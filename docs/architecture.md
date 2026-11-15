# Architecture of 3.0 (Go)

Status: approved design, implementation in progress on `v3-go`.

## 1. Goals

- A REST API for **Uptime Kuma 2.x**, written in Go, with no dependency on a third-party Uptime Kuma
  client. The Socket.IO client is part of this project.
- **Same REST contract as 2.0** (routes, JSON bodies, status codes, error format, environment
  variables, `/db` volume). A 2.0 client must keep working against 3.0. Additions are allowed,
  silent changes are not.
- Fast by construction: reads never wait on Uptime Kuma, requests to Uptime Kuma run concurrently
  on one connection, and the targets in section 8 are measured in CI.

Non-goals: supporting Uptime Kuma 1.x (the Python 2.x line does that), a web UI, multi-instance
fan-out (one API process talks to one Uptime Kuma).

## 2. Facts about Uptime Kuma 2.x this design relies on

Verified in the server source of 2.5.11 (`server/server.js`, `server/socket-handlers/`,
`server/client.js`, `server/rate-limiter.js`):

- Management is only possible over Socket.IO 4.8 (Engine.IO protocol 4). There is no REST API.
- 84 client-to-server events. 68 of the 69 events of 1.23.17 still exist; `uploadBackup` is gone.
  New: incidents (`getIncidentHistory`, `editIncident`, `deleteIncident`, `resolveIncident`), remote
  browsers, `getMonitorChartData`, paged important heartbeats, `checkDomain`, `getPushExample`.
- Every command answers through the Socket.IO ack callback with `{ok: bool, msg?: string, ...}`.
- Logins are rate limited (20 per minute). A session token returned by `login` can be reused with
  `loginByToken`.
- After login the server pushes its state and keeps pushing changes: `monitorList`,
  `updateMonitorIntoList`, `deleteMonitorFromList`, `heartbeatList`, `heartbeat`, `uptime`,
  `avgPing`, `certInfo`, `notificationList`, `proxyList`, `dockerHostList`, `maintenanceList`,
  `statusPageList`, `apiKeyList`, `remoteBrowserList`, `monitorTypeList`, `info`.

Payload shapes are recorded from a real server in `internal/kuma/testdata/` (phase 0) and are the
reference for types and tests. Do not guess a payload: record it.

## 3. Components

```
                 ┌──────────────────────── uptime-kuma-api (one process) ───────────────────────┐
HTTP clients ──▶ │ httpapi ──reads──▶ kuma.Cache ◀──push events── kuma.Session ◀═ WebSocket ═▶ │ Uptime Kuma 2.x
                 │    │                                                 ▲                       │
                 │    ├──writes / uncached reads──────────────────────────┘                       │
                 │    └──auth──▶ auth (JWT, bcrypt) ──▶ store (SQLite, /db)                      │
                 └───────────────────────────────────────────────────────────────────────────────┘
```

| Package | Responsibility |
| ------- | -------------- |
| `cmd/uptime-kuma-api` | `main`: load config, wire components, serve, graceful shutdown |
| `internal/config` | Environment variables, same names and defaults as 2.0 |
| `internal/kuma/sio` | Engine.IO 4 + Socket.IO 5 packet codec and a WebSocket-only client. Knows nothing about Uptime Kuma |
| `internal/kuma` | `Session` (connect, login, reconnect, `Call`), `Cache` (state built from pushes), typed commands |
| `internal/auth` | Password hashing (bcrypt), JWT (HS256), secret key persistence |
| `internal/store` | Users in SQLite; reads the 1.x/2.0 database unchanged |
| `internal/httpapi` | Routes, request/response types, middleware, error mapping, OpenAPI |

Dependencies point downwards only. `httpapi` depends on interfaces declared where they are used
(`type Kuma interface{...}` in `httpapi`), so handlers are tested without a socket.

## 4. Uptime Kuma session

- **Transport**: WebSocket only (`/socket.io/?EIO=4&transport=websocket`), no long-polling. Answers
  Engine.IO pings; a missed ping window (pingInterval + pingTimeout) closes the connection.
- **Goroutines**: one reader, one writer. The writer owns the connection for writes and takes
  frames from a buffered channel. Nothing else touches the socket.
- **Calls**: `Call(ctx, event, args...) (json.RawMessage, error)` allocates an ack id, registers a
  one-shot channel in a map guarded by a mutex, enqueues the frame and waits for the ack or
  `ctx.Done()`. Calls are concurrent; there is no global request lock.
- **Deadlines**: every call carries `KUMA_TIMEOUT`. On expiry the ack entry is removed and the
  caller gets `ErrTimeout` (HTTP 504). A late ack for a removed id is dropped.
- **Connection**: established lazily on first use and kept. Concurrent first users share one
  attempt. The API starts and serves `/health` without Uptime Kuma.
- **Login**: `login` with username/password once, keep the returned token in memory, use
  `loginByToken` on reconnects. If the token is rejected, fall back to `login` once.
  A server that requires 2FA yields a clear configuration error (HTTP 503), never a retry loop.
- **Reconnect**: exponential backoff with jitter (250 ms to 15 s). In-flight calls fail with
  `ErrUnavailable` (HTTP 503). Idempotent reads are retried once after reconnect; writes are never
  retried automatically.
- **Errors**: `{ok:false,msg}` becomes a typed error. The mapping to 400 / 404 / 422 is the 2.0
  mapping (`app/errors.py`), extended with the 2.x messages found in phase 0.

## 5. State cache

- One struct per pushed list, replaced or patched under an `RWMutex` by the reader goroutine.
- Each route keeps the **encoded JSON** of its answer for one list version, built on the first
  read of that version (once, however many requests wait for it), never from an unsynced read.
  A cached `GET` writes those bytes; the route allocates nothing per request.
- Responses carry a strong `ETag` (hash of the bytes); `If-None-Match` returns `304`.
- **Read-your-writes**: every list has a version counter. A write handler notes the version before
  the call and, after a successful ack, waits (bounded by the request deadline) until the counter
  has advanced or, for commands that Kuma does not echo with a push, refreshes that list itself.
- Cached: monitors, latest heartbeats, uptime, average ping, certificate info, notifications,
  proxies, docker hosts, maintenances, status page list, API keys, remote browsers, server info.
- Not cached (no push exists): tags, settings, monitor beats over a period, chart data, status
  page details, maintenance monitors/status pages, database size. Identical concurrent reads are
  collapsed into one call to Uptime Kuma.
- Before the first login completes, cached reads wait for the initial state (bounded by the
  deadline) rather than returning empty lists.
- Only the latest heartbeat of each monitor is kept; memory does not grow with uptime.

## 6. HTTP layer

- Standard library `net/http` server and `ServeMux` (method + wildcard patterns).
- Request and response types are Go structs; the OpenAPI 3.1 document and the docs page at `/docs`
  are generated from them (huma v2 on the standard mux). No hand-written spec.
- Monitor, notification, proxy, docker host, maintenance and status page bodies are forwarded as
  objects: known fields are documented and validated for type, unknown fields pass through, and
  only fields present in the request are sent. Uptime Kuma is the authority on semantic validation.
- Creating a monitor needs the full object Uptime Kuma's own UI sends. The defaults live in one
  table (`internal/kuma/defaults.go`) derived from the 2.x UI, covered by an end-to-end test per
  monitor type that the test server can run.
- Middleware, in order: request id, panic recovery, access log (slog, JSON), CORS
  (`BACKEND_CORS_ORIGINS`), body size limit, authentication.
- Server timeouts are set (`ReadHeaderTimeout`, `ReadTimeout`, `WriteTimeout`, `IdleTimeout`), and
  shutdown drains in-flight requests on SIGTERM.
- Errors are always `{"detail": "..."}` with 400 / 401 / 403 / 404 / 409 / 422 / 503 / 504 as in 2.0.
  Internal errors are logged with the request id and never returned verbatim.

## 7. Authentication and users

Unchanged behaviour from 2.0 (`app/security.py`, `app/database.py`, `app/routers/users.py`):

- `POST /login/access-token` (OAuth2 password form) returns a JWT: HS256, `sub` = username, `exp`.
- `SECRET_KEY` from the environment, else generated once and stored at `<DB dir>/secret_key`
  (mode 0600), so tokens issued by 2.0 stay valid after upgrading when the volume is kept.
- Users live in SQLite at `DB_PATH` in the existing `users` table; bcrypt hashes written by 1.x and
  2.0 verify unchanged. The `admin` user is created at start and its password is set to
  `ADMIN_PASSWORD` at every start. Only `admin` manages users; `admin` cannot be deleted.
- A deleted user's token is rejected (the user is looked up per request, through a small in-memory
  cache invalidated on user writes).
- SQLite driver: pure Go (`modernc.org/sqlite`), so the binary is static and `CGO_ENABLED=0`.

## 8. Performance targets

Measured by `make bench` (in-process benchmarks) and a load test in CI against the container with a
real Uptime Kuma holding 200 monitors. The Python 2.0 image is measured under the same load as the
baseline and both results are published in the release notes.

| Measure | Target |
| ------- | ------ |
| Cached `GET /monitors`, p99 at 5,000 req/s, 64 connections | under 2 ms |
| Allocations per cached `GET` in the handler | 0 beyond net/http's own |
| Idle resident memory | under 25 MB |
| Image size (compressed) | under 20 MB |
| Start to healthy | under 200 ms |

Rules that keep it that way: no per-request login or connection, no reflection-heavy work on the
read path, no unbounded buffers or goroutines per request, bcrypt only on login.

## 9. Quality gates

- `go vet`, `staticcheck`, `golangci-lint`, `gofmt`; `govulncheck` in CI.
- Unit tests with the race detector; coverage of `internal/...` at 90% or more.
- `internal/kuma/sio`: table tests from the recorded frames and a fuzz test on the packet parser.
- `internal/kuma`: tests against an in-process fake server that speaks real Socket.IO over a real
  WebSocket, including disconnects mid-call, late acks and token rejection.
- The existing black-box suite (`tests/e2e`, HTTP only) is the acceptance test and runs against
  Uptime Kuma `2.0`, the newest `2.x` patch and the moving `2` tag.
- Supply chain: pinned module versions, minimal distroless image, non-root, SBOM and provenance on
  release images.

## 10. Allowed dependencies

| Module | Why |
| ------ | --- |
| `github.com/coder/websocket` | WebSocket protocol |
| `modernc.org/sqlite` | SQLite without cgo |
| `golang.org/x/crypto` | bcrypt |
| `github.com/danielgtaylor/huma/v2` | OpenAPI from Go types |

JWT (HS256 only), CORS, configuration and logging use the standard library. Adding a dependency
needs a line in this table with the reason.

## 11. Delivery phases

| Phase | Content | Done when |
| ----- | ------- | --------- |
| 0 | Record Uptime Kuma 2.5.11 frames and payloads; list what the 2.0 suite fails on | fixtures and the gap list are committed |
| 1 | `sio` codec and client, `kuma.Session` | unit, fuzz and race tests pass; login + one command works against a real 2.5.11 |
| 2 | config, store, auth, users, `/health`, `/docs` | 2.0 user database upgrades; auth part of the acceptance suite passes |
| 3 | All 2.0 routes on Uptime Kuma 2.x | the whole acceptance suite passes on 2.5.11 |
| 4 | Cache, ETag, read-your-writes, benchmarks | section 8 targets met and recorded |
| 5 | New 2.x resources: incidents, remote browsers, chart data, API keys | acceptance tests added and passing |
| 6 | Dockerfile, CI matrix, release workflow, README, migration guide; Python sources removed | `v3.0.0-rc.1` image built by CI |

Module path: `github.com/MedAziz11/Uptime-Kuma-Web-API/v3`. The Python 2.x line continues on a
`2.x` branch for Uptime Kuma 1.x users (security fixes only).
