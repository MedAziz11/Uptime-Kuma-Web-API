# Upgrading from 2.0 to 3.0

3.0 is a rewrite in Go for **Uptime Kuma 2.x**. It keeps 2.0's routes, JSON bodies, status codes,
environment variables and `/db` volume. The differences are listed below with what a client has to
change; most need no change. The 2.x line (Python, Uptime Kuma 1.x) continues on the branch `2.x`
with security fixes only.

## Before upgrading

- **Uptime Kuma must be 2.x.** 3.0 does not work with Uptime Kuma 1.x, and 2.0 cannot create
  monitors on Uptime Kuma 2.x. Upgrade Uptime Kuma first (with its own backup and migration), then
  the API.
- **Use a current Uptime Kuma 2.x release.** The acceptance suite passes on 2.5.11. On 2.0.0,
  creating a monitor and reading the incident history fail (see [Known limitations](#known-limitations)).
- **Back up the `/db` volume**, e.g.
  `docker run --rm -v <volume>:/db:ro -v "$PWD":/backup alpine tar czf /backup/api-db.tgz -C /db .`
- The Uptime Kuma account must still not have two-factor authentication enabled.

## Upgrade (Docker)

1. Change the image tag from `medaziz11/uptimekuma_restapi:v2.0.0` (or `v2`, `latest`) to
   `medaziz11/uptimekuma_restapi:3.0.0-rc.1` (`3` once 3.0.0 is released). 3.x tags have no `v`
   prefix.
2. Keep the environment variables and the `/db` volume (or bind mount) as they are.
3. Start the container. `GET /health` answers `{"status":"ok","version":"3.0.0-rc.1"}`.

What carries over unchanged:

- Users, their passwords (bcrypt hashes written by 1.x and 2.0) and `created_at`; the `admin`
  password is set from `ADMIN_PASSWORD` at every start, as in 2.0.
- Tokens issued by 2.0: the signing key is `SECRET_KEY` or the `secret_key` file 2.0 generated in
  the volume.
- File ownership: the image runs as uid 100, gid 101, the `appuser:appgroup` of the 2.0 image, and
  reads and writes the files 2.0 created (`db.sqlite3`, `secret_key` with mode 0600). If you ran
  2.0 with `--user`, keep the same option.

Checked on a copy of a volume written by the 2.0 container: an existing user logs in, tokens 2.0
issued are accepted, the user list is identical.

What changes in the deployment:

| 2.0 | 3.0 | Change needed |
| --- | --- | ------------- |
| Python on Alpine, with a shell | one static binary on distroless, no shell | `docker exec ... sh` and scripts that run `python` in the container no longer work |
| `CMD ["uvicorn", ...]`, port fixed at 8000 | `ENTRYPOINT ["/uptime-kuma-api"]`; `LISTEN_ADDR` (default `:8000`) | Replace a custom `command:` that ran uvicorn; set `LISTEN_ADDR` to change the port |
| `HEALTHCHECK` with `python -c ...` | `HEALTHCHECK` with `/uptime-kuma-api healthcheck` | Health checks defined outside the image: use `/uptime-kuma-api healthcheck` or `GET /health` |
| Settings also read from a `.env` file in the working directory | environment variables only | Pass the variables to the container (`env_file:` in compose) |
| Access log by uvicorn (text) | JSON lines on standard error (`slog`) | Adjust log parsing |
| Image tags `v2.0.0`, `v2.0`, `v2` | `3.0.0`, `3.0`, `3`; a release candidate only gets its own tag | Pin `3` or an exact version |
| Images for amd64 and arm64 | same | — |

## Rollback

Stop 3.0 and start the 2.0 image with the same volume and environment. 3.0 does not change the
database schema: users created and tokens issued by 3.0 work in 2.0 (checked on a copy of a volume
written by 3.0). 2.0 needs Uptime Kuma 1.x to work fully, so a complete rollback also restores
Uptime Kuma 1.x from its backup. If anything goes wrong, restore the `/db` backup.

## Differences from 2.0

The rule: 3.0 reproduces 2.0's routes, status codes and response shapes. It does not reproduce
2.0's error texts byte for byte. Clients should branch on status codes, not on `detail` texts.

### Routes

| Area | 2.0 | 3.0 | What a client must change |
| ---- | --- | --- | ------------------------- |
| `POST /settings/upload_backup` | 504 on Uptime Kuma 2.x (422 for a bad `import_handle`) | always `410 Gone` | Stop calling it: Uptime Kuma 2.x removed backup import |
| New routes | — | `/api_keys`, `/remote_browsers`, `/push_examples/{language}`, `/monitors/{id}/chart`, `/monitors/{id}/important_beats`, `/statuspages/{slug}/incidents` (history, edit, delete, resolve) | Nothing; see `/docs` |
| `POST /monitors` defaults | uptime-kuma-api's (`maxretries` 1, ...) | Uptime Kuma 2.x UI's (`maxretries` 0, `timeout` 48, per-type ports, ...) | Send the fields you depend on explicitly |
| Monitor types, `accepted_statuscodes` | 1.23's types; codes 100–999 | also 2.x's types (`websocket-upgrade`, `snmp`, ...) and codes 1000–4999 | Nothing |
| `timeout` of a monitor | integer | number | Nothing (decimals are accepted) |
| `GET /monitors/{id}` | `getMonitor` | the pushed monitor list (same fields; `path` and `pathName` filled in) | Nothing |
| `GET /ping`, `/uptime`, `/cert_info` | waited up to 2 s for pushes; kept deleted monitors | the pushed state at once; deleted monitors removed | Do not expect entries of deleted monitors |
| `GET /statuspages/{slug}` `incident` | failed on Uptime Kuma 2.x | the newest pinned incident, or null | Nothing |
| `POST /statuspages/{slug}` | failed on Uptime Kuma 2.x | merges into Uptime Kuma's current configuration; `googleAnalyticsId` sets `analyticsId` and `analyticsType: google`; an invalid theme is 422 | Nothing |
| `POST /monitors/{id}/tag` with an unknown tag | 404 `Not found` | 404 `tag does not exist` | Nothing (same status) |
| `POST /maintenance/{id}/monitors`, `DELETE /statuspages/{slug}/incident/unpin` with an unknown id or slug | Uptime Kuma's error or success | 404 | Handle 404 |
| `PATCH /notifications/{id}` changing `type` | dropped the old provider's options | keeps them | Remove old options yourself if needed |

### Request validation

| Area | 2.0 | 3.0 | What a client must change |
| ---- | --- | --- | ------------------------- |
| Lax coercion (`"5"` for an integer, `0`/`1` for a boolean) | accepted | 422 (`int_parsing`, `bool_type`, ...) | Send JSON numbers and booleans |
| Documented fields of notifications, proxies, docker hosts | not type-checked | type-checked (422) | Send the documented JSON types |
| Fields a body does not document | monitors: passed on; maintenances, status pages: dropped; proxies, docker hosts: 422 | passed on to Uptime Kuma | Do not send fields you do not want stored |
| Notification options the API does not document | 422 `unknown argument` | passed on to Uptime Kuma, which stores them | Nothing |
| Texts of the argument checks (422 with a text detail) | Python's `TypeError`/`ValueError` texts | similar English texts | Do not match on the text |
| 422 for malformed JSON | Python's message and character position | Go's message and byte offset; same `type` (`json_invalid`) and shape | Do not match on the message |
| Order of keys in the `input` echoed by a 422 | request order | sorted | Do not depend on the order |
| JSON nested deeper than Python's recursion limit | 400 | 422 `json_invalid` | Nothing |
| `ctx` of a query parameter's `greater_than` | `{"gt": 0.0}` | `{"gt": 0}` | Nothing (same number) |

### Authentication and errors

| Area | 2.0 | 3.0 | What a client must change |
| ---- | --- | --- | ------------------------- |
| Path no route serves, without a valid token | 404 | 401 | Nothing, unless you probed paths without a token |
| Malformed JSON body without a token | 422 | 401 | Send the token |
| Login or user creation while bcrypt is busy for 3 s | waited | `503 Too many logins in progress, retry later`, `Retry-After: 1` | Retry after `Retry-After` |
| Login or authenticated request right after start | not served yet | waits at most 3 s for the admin setup, then `503 The API is starting, retry later` | Retry on 503 |
| Uptime Kuma's success messages | the translation key Uptime Kuma 2.x sends (`successAdded`, with `msgi18n`) | the English text of Uptime Kuma's UI (`Added Successfully.`), no `msgi18n` | Nothing (what 2.0 returned with Uptime Kuma 1.x) |
| Raw JavaScript or SQL error from Uptime Kuma | 400 with the raw text | 400 `Uptime Kuma could not process the request`; the text is in the API's log with the request id | Read the log for details |
| 503 details | Python exception texts | `Uptime Kuma is unavailable`, `Uptime Kuma rejected the login`, or the 2FA explanation | Do not match on the text |

### HTTP

| Area | 2.0 | 3.0 | What a client must change |
| ---- | --- | --- | ------------------------- |
| Reads served from memory (`/monitors`, `/monitors/{id}`, `/notifications[/{id}]`, `/proxies[/{id}]`, `/docker_hosts[/{id}]`, `/maintenance`, `/statuspages`, `/uptime`, `/ping`, `/cert_info`, `/info`, `/api_keys[/{id}]`, `/remote_browsers[/{id}]`) | no validator | strong `ETag`, `Cache-Control: private, no-cache`, `If-None-Match` answers `304`; always `Content-Length` | Nothing; send `If-None-Match` to save bandwidth |
| `HEAD` on a `GET` route | 405 | answered like `GET` | Nothing |
| `Allow` header of a 405 | the first route's methods | every method of the path | Nothing |
| `/openapi.json` content type | `application/json` | `application/openapi+json` | Accept both |
| `/docs` content type | `text/html; charset=utf-8` | `text/html` | Nothing |

## Known limitations

- **Uptime Kuma 2.0.0**: creating a monitor fails with 400 (`table monitor has no column named
  bearer_token` in the API's log) and `GET /statuspages/{slug}/incidents` answers 504 (2.0.0 has no
  `getIncidentHistory`). The monitor defaults are those of Uptime Kuma 2.5's UI; according to the
  Uptime Kuma sources, they need columns added in 2.4.0 (`bearer_token`, `gamedig_token`) and 2.5.0
  (`ntp_*`), and the incident history exists from 2.1.0. Only 2.0.0 and 2.5.11 were tested.
- The status page list (`GET /statuspages`) is pushed by Uptime Kuma only at login. Changes made by
  the API are applied at once; changes made in Uptime Kuma's UI show after the API's next
  reconnect.
- The other pushed lists (notifications, proxies, docker hosts, API keys, remote browsers,
  maintenances) are applied as they arrive. With SQLite they arrive in order; with MariaDB, and for
  two concurrent maintenance writes, an older list can arrive last and stays until the next push
  of that list.
- `GET /statuspages/{slug}/incidents` passes Uptime Kuma's paging through: incidents created in the
  same second as the last one of a page are not returned on the next page.
- `checkDomain` (domain expiry lookups) is not exposed.
- Performance: the p99 target of 2 ms for `GET /monitors` with full 532 kB bodies at 5,000 req/s
  is not met on the test machine (2.8 to 17.1 ms); see [performance.md](performance.md).

Details on how 3.0 talks to Uptime Kuma 2.x: [kuma-2x-notes.md](kuma-2x-notes.md).
