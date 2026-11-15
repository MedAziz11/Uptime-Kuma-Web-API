# Uptime Kuma 2.x: observed behaviour

Phase 0 of the 3.0 rewrite. Everything here was observed on `louislam/uptime-kuma:2.5.11`
(13 November 2026) or read in its server source. Recordings: `internal/kuma/testdata/2.5.11/`
(`bootstrap/` on a fresh instance, `lifecycle/` creating and deleting one of each resource), made
with the scripts in `tools/kuma-capture/`. Secrets in the recordings are replaced by `<redacted>`.

## The 2.0 (Python) API against Uptime Kuma 2.5.11

Acceptance suite `tests/e2e`: 9 passed, 2 failed, 5 errors.

| Result | Cause | Consequence for 3.0 |
| ------ | ----- | ------------------- |
| 5 errors: `POST /monitors` → 400, `NOT NULL constraint failed: monitor.conditions` | 2.x requires `conditions` (and other new fields) on `add` | Monitor defaults come from the 2.x UI defaults, see below |
| `test_backup_import` → 504 | The `uploadBackup` event no longer exists; nothing answers | `POST /settings/upload_backup` is removed in 3.0 and answers `410 Gone` with an explanation |
| `test_server_info` | The test asserts the version starts with `1.` | Fix the test |

## Connection

- **Handlers are registered late.** The server's connection callback awaits `sendInfo` before it
  registers any `socket.on(...)`. A command sent right after the Socket.IO CONNECT is dropped and
  never acked. The server emits `loginRequired` (or `autoLogin` when authentication is disabled)
  as the last step of that callback: wait for one of them before the first command.
- `autoLogin` means the socket is already logged in: do not call `login`.
- On connect the server pushes `info` (`{primaryBaseURL, serverTimezone, serverTimezoneOffset}`
  before login; the version is only included after login) and, on a fresh instance, `setup`.
- `login` acks `{ok:true, token}`; `loginByToken` with that token acks `{ok:true}`; an invalid
  token acks `{ok:false, msg:"authInvalidToken", msgi18n:true}`.
- A command on a socket that is not logged in acks `{ok:false, msg:"You are not logged in."}`.

## Acks

- Shape: `{ok, msg?, msgi18n?, ...}`. When `msgi18n` is true, `msg` is a translation key
  (`successAdded`, `successDeleted`, `successPaused`, `successResumed`, `authInvalidToken`,
  `authIncorrectCreds`) or the English text `Saved.`. Map on keys, never show them to clients.
- **Deleting something that does not exist succeeds**: `deleteMonitor(999999)` and
  `deleteTag(999999)` ack `{ok:true, msg:"successDeleted"}`. The API must check existence itself to
  answer 404.
- Unknown ids on reads still fail with raw errors: `getMonitor(999999)` →
  `Cannot read properties of null (reading 'id')`; `getStatusPage("nope")` → `No slug?`.
- `add` with a partial object fails inside the server (`Cannot read properties of undefined
  (reading 'every')`): it needs the complete object.
- Ids come back under different keys: `monitorID` (add, editMonitor), `maintenanceID`, `id`
  (notification, proxy, docker host), `tag` (object), `slug`, `keyID` + `key` (API key; the clear
  key is only returned here).

## Monitor defaults

`add` expects what the UI sends. The UI's `monitorDefaults` (`src/pages/EditMonitor.vue`) is
reproduced in `tools/kuma-capture/lifecycle.py` (`MONITOR_DEFAULTS`) and works for `http`, `group`
and `push`. Required beyond 1.x: `conditions: []`, `kafkaProducerBrokers: []`,
`kafkaProducerSaslOptions`, `rabbitmqNodes: []`, and `accepted_statuscodes` as strings.
`editMonitor` takes the full object returned by `getMonitor`.

## Pushes

After login: `monitorList` (object keyed by id), `maintenanceList` (object keyed by id),
`notificationList`, `proxyList`, `dockerHostList`, `apiKeyList`, `remoteBrowserList` (arrays),
`statusPageList` (object keyed by id), `monitorTypeList`, `info`, and per monitor `heartbeatList`.

| Command | Pushes that follow |
| ------- | ------------------ |
| `add`, `editMonitor` | `updateMonitorIntoList` (`{id: monitor}`), then `heartbeat`, `avgPing`, `uptime` ×3 (24, 720, "1y") once the first check ran |
| `pauseMonitor`, `resumeMonitor`, `addMonitorTag`, `deleteMonitorTag` | `updateMonitorIntoList` |
| `deleteMonitor` | `deleteMonitorFromList` (id) |
| `addNotification` / `addProxy` / `addDockerHost` / `addAPIKey` / `addMaintenance` | the matching full list |
| `addTag`, `editTag`, `deleteTag` | none |
| `addMonitorMaintenance` | none |
| `addStatusPage`, `saveStatusPage`, `deleteStatusPage` | **none**: `statusPageList` is only sent at login |

Consequences for the cache: tags are not cacheable from pushes; the status page list must be
patched by the API after its own writes (and is stale for changes made in the Kuma UI until the
next reconnect, so status page reads go to `getStatusPage` and the list is refreshed by logging in
again on a short-lived second socket only if this proves necessary).

`getMonitorList`, `getMaintenanceList` and `getAPIKeyList` ack `{ok:true}` and deliver the data as
a push, not in the ack.

## New in 2.x (not in the 2.0 API)

`getMonitorChartData(id, hours)` → `{data:[{up, down, avgPing, minPing, maxPing, timestamp}]}`;
`monitorImportantHeartbeatListCount` / `...Paged(id, offset, count)`; incidents on status pages
(`postIncident`, `getIncidentHistory`, `editIncident`, `deleteIncident`, `resolveIncident`,
`unpinIncident`); remote browsers; API keys (`addAPIKey`, `getAPIKeyList`, enable/disable/delete).

## Differences from 2.0

The rule: 3.0 reproduces 2.0's routes, status codes and response shapes. It does not reproduce
2.0's error texts byte for byte. Known and deliberate differences:

| Area | 2.0 | 3.0 | Why |
| ---- | --- | --- | --- |
| Path no route serves, without a valid token | 404 | 401 | Authentication runs before routing (default deny) |
| `HEAD` on a `GET` route | 405 | answered like `GET` | `net/http` serves `HEAD` for `GET` routes |
| `/openapi.json` content type | `application/json` | `application/openapi+json` | huma's document handler |
| `/docs` content type | `text/html; charset=utf-8` | `text/html` | huma's docs handler |
| `Allow` header of a 405 | first route's methods | every method of the path | |
| Malformed JSON body without a token | 422 | 401 | The token is checked before the body is read |
| 422 for a malformed JSON body | Python's message and character position | Go's message and byte offset | Same `type` (`json_invalid`) and shape |
| Order of keys in the `input` echoed by a 422 | request order | sorted | `encoding/json` maps |
| JSON nested deeper than Python's recursion limit | 400 | 422 `json_invalid` (beyond Go's limit of 10000) | |
