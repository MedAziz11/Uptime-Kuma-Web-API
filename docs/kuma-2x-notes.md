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
| Kuma's success messages | the translation key Kuma 2.x sends (`successAdded`, with `msgi18n`) | the English text of Kuma's UI (`Added Successfully.`), no `msgi18n` | What 2.0's clients got from Kuma 1.x |
| Raw JavaScript or SQL error from Kuma | 400 with the raw text | 400 `Uptime Kuma could not process the request`; the text is logged with the request id | Internal details are not returned |
| 503 details | Python exception texts | `Uptime Kuma is unavailable`, `Uptime Kuma rejected the login`, or the 2FA explanation | |
| Texts of 2.0's argument checks (422 with a text detail) | Python's `TypeError`/`ValueError` texts | similar English texts, e.g. `missing 2 required argument: 'protocol', 'port'` | Same status and shape |
| Lax coercion in bodies (`"5"` for an integer, `0`/`1` for a boolean) | accepted by pydantic | 422 (`int_parsing`, `bool_type`, ...) | Fields are checked for their JSON type only |
| `timeout` of a monitor | integer | number | Kuma 2.x accepts decimals |
| Fields a body does not document | monitors: passed on; maintenances, status pages: dropped; proxies, docker hosts: 422 | passed on to Kuma (architecture §6) | |
| Notification options the API does not document | 422 `unknown argument` (uptime-kuma-api 1.2.1's list of provider options) | passed on to Kuma, which stores them | A static list lags behind Kuma's providers (§6); the acceptance suite asserts the pass-through |
| Documented fields of notifications, proxies, docker hosts | not type-checked | type-checked (422) | §6 |
| `PATCH /notifications/{id}` changing `type` | dropped the old provider's options | keeps them | No per-provider option list for 2.x |
| `POST /monitors` defaults | uptime-kuma-api's (`maxretries` 1, ...) | Kuma 2.x UI's (`defaults.go`: `maxretries` 0, `timeout` 48, per-type ports, ...) | 2.0 could not create monitors on 2.x at all |
| Monitor types, `accepted_statuscodes` | 1.23's types; codes 100–999 | also 2.x's types (`websocket-upgrade`, `snmp`, ...) and codes 1000–4999 (WebSocket close codes) | |
| `GET /ping`, `/uptime`, `/cert_info` | waited up to 2 s for pushes; kept deleted monitors | the pushed state at once; deleted monitors removed | |
| `GET /monitors/{id}` | `getMonitor` | the pushed monitor list (same fields; `path` and `pathName` filled in) | |
| `GET /statuspages/{slug}` `incident` | (failed on 2.x: Kuma now lists `incidents`) | the newest pinned incident, or null | |
| `POST /statuspages/{slug}` | (failed on 2.x) | merges into Kuma's current configuration; `googleAnalyticsId` sets `analyticsId` and `analyticsType: google`; an invalid theme is 422 | |
| Incident routes | re-saved the page to clear Kuma's response cache | read the public page with `Cache-Control: no-cache` | No extra write |
| `POST /settings/upload_backup` | 504 on 2.x (422 for a bad `import_handle`) | always 410 Gone | Kuma 2.x removed backup import |
| `POST /monitors/{id}/tag` with an unknown tag | 404 `Not found` from Kuma's foreign key error | 404 `tag does not exist`, checked first | |
| `POST /maintenance/{id}/monitors`, `DELETE /statuspages/{slug}/incident/unpin` with an unknown id or slug | Kuma's error or success | 404, checked first | Kuma would store links to nothing or answer success |
| `ctx` of a query parameter's `greater_than` | `{"gt": 0.0}` | `{"gt": 0}` | Same number in JSON |

## How 3.0 reads and writes (phase 3)

- `kuma.State` keeps what Kuma pushes; reads of pushed lists call `Client.Sync` (wait for the
  login, 503 while Kuma is unreachable) and then read `State`. `State` is reset before each
  connection; the login pushes everything again.
- A write returns once a following read reflects it: for a list Kuma pushes after the command,
  the write waits (bounded by `KUMA_TIMEOUT`) until the list's version grows; status pages are
  read back with `getStatusPage`; after a tag edit or delete the monitor list is fetched again
  (`getMonitorList`), since monitors carry their tags.
- `GET /api/status-page/{slug}` is cached by Kuma for minutes and the cache is not cleared by
  `postIncident` or `unpinIncident`; Kuma's cache honours `Cache-Control: no-cache`, which the API
  sends.
- Every read of `State` reports, under the same lock, whether `State` holds a complete login
  state (false from the reset before a connection to the end of the next login). A route answers
  503 when it does not, never an empty list.
- Monitor pushes can arrive out of order: Kuma builds `monitorList` and `updateMonitorIntoList`
  from database reads with many awaits and emits them when done, so a list read before a change
  committed can arrive after the push of that change (a deleted monitor comes back, a new one
  disappears, an edit is reverted). Monitor ids are never reused: `State` remembers the ids
  deleted on the current connection and ignores them in later monitor and statistics pushes. A
  full list is trusted only if no other monitor push came close to it: our own `getMonitorList`
  is fetched again (at most 3 times) if an update, a deletion or another list arrived between the
  request and the answer; a list we did not ask for is verified with one debounced fetch only if
  an update or deletion was applied less than 2 s before it (verifying every unrequested list
  would never settle with two API instances on one Kuma user).

## Known limitations

- The other full lists (`notificationList`, `proxyList`, `dockerHostList`, `apiKeyList`,
  `maintenanceList`) are applied as they arrive. Kuma builds the first four from one database
  query each and emits right after it (`server/client.js`): they arrive in order as long as the
  database answers queries in order, which holds for SQLite through Kuma's single connection but
  not necessarily for MariaDB with a connection pool. `maintenanceList` is built with one await per
  maintenance (`getMaintenanceJSONList`), so two concurrent maintenance writes can make an older
  list arrive last. `getMaintenanceList` and `getAPIKeyList` could re-fetch, but nothing verifies
  these lists; a stale one is corrected by the next push of the same list (the next write of that
  kind, or a login of the same Kuma user).
- `TestRealKumaMonitorListConsistency` checks the monitor list after a concurrent load on a real
  Kuma, but on the test instance it did not reproduce the out-of-order push without the fix
  either; the orders themselves are covered by the fake Kuma's tests (`internal/kuma/race_test.go`).
