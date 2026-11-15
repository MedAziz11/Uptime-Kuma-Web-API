# Performance (phase 4)

Measured on 15 November 2026 against Uptime Kuma 2.5.11 holding 200 group monitors (2.6 kB each
as Kuma pushes them; `GET /monitors` answers 532,082 bytes).

## Environment

- Laptop, Intel Core i5-1335U (2 performance + 8 efficiency cores, 12 threads), Windows 11,
  Docker Desktop. The Docker VM has 12 CPUs and 8 GB. Desktop applications ran on the host
  during the measurements; repeated runs of the same load differ by up to 3x at p99 (all runs are
  listed below).
- The load generator (`tools/loadtest`) ran in its own container on the Docker network `ukapi-p0`
  and sent to `http://ukapi-p4-go:8000`; the API ran in a `golang:1.27-alpine` container with the
  static binary (`CGO_ENABLED=0`), `GOMAXPROCS` unset (12), `LOG_LEVEL=info`.
- In-process benchmarks ran in the `ukapi-go:dev` container (Go 1.27) on the same VM.

## How to reproduce

```sh
# In-process benchmarks (full middleware chain with 200 recorded monitors; the route alone)
go test -run XXX -bench . -benchmem ./internal/httpapi
go test -run XXX -bench Subject -benchmem ./internal/auth

# Build the API and the load generator
CGO_ENABLED=0 go build -trimpath -o out/uptime-kuma-api ./cmd/uptime-kuma-api
CGO_ENABLED=0 go build -trimpath -o out/loadtest ./tools/loadtest

# Seed 200 group monitors through the API (and 2 tags for the tag test), remove them afterwards.
# LOADTEST_PASSWORD is the API's admin password (LOADTEST_TOKEN may be given instead).
loadtest seed -url http://ukapi-p4-go:8000 -state seed.json -monitors 200 -tags 2
loadtest unseed -url http://ukapi-p4-go:8000 -state seed.json

# Cached GET /monitors, open loop: 5,000 req/s, 64 keep-alive connections
loadtest -url http://ukapi-p4-go:8000 -path /monitors -rate 5000 -conns 64 -duration 20s -warmup 5s
# ... revalidated with the last ETag seen (every request answers 304)
loadtest ... -revalidate 1
# ... while 24 connections log in continuously with a wrong password
loadtest ... -flood 24
# 12 concurrent tag writers (closed loop)
loadtest -path /tags/ID -method PATCH -body '{"color":"#2563eb"}' -rate 0 -conns 12 -duration 30s
```

Latencies are measured from each request's due time (open loop), so queueing in the server is
included. "late" in the raw output counts requests sent more than 1 ms after their due time
because all 64 connections were busy.

Start to healthy: a helper starts the binary (copied into the container's own filesystem; run
from the Windows bind mount, paging the binary in took about 400 ms) and polls `/health` every
millisecond; one start on an empty database, then four restarts. Idle memory: `VmRSS` of the
process 15 s after it connected to Kuma and answered one `GET /monitors`.

## Targets

| Measure | Target | Before (phase 3) | After | Python 2.0 |
| ------- | ------ | ---------------- | ----- | ---------- |
| `GET /monitors`, 200 monitors, p99 at 5,000 req/s, 64 connections | < 2 ms | saturates at 150-200 req/s (p99 0.56 s at 200 req/s, 10.8 s at 400 req/s) | full body: p99 2.8, 6.4, 5.9, 17.1 ms (4 runs); revalidated (304): p99 1.44, 1.45 ms | saturates between 5 and 10 req/s: p50 2.2 s at 5 req/s, 19 s at 10 req/s |
| same, during a login flood (24 connections) | < 2 ms | revalidated: p50 27 s (reads starved) | revalidated: p99 5.5 to 19 ms over runs (16.8 ms in the validation run); full body: p99 17.7 ms | — |
| Allocations per cached `GET` in the handler | 0 beyond net/http's | 53,417 (route alone) | 0 (`/monitors`, 304); 1 for `/monitors/{id}`, which is net/http's `ServeMux` wildcard match | — |
| Idle resident memory | < 25 MB | 24.8 MB and 28.9 MB (two runs) | 22.9 MB (12.0 MB anonymous, 10.9 MB file-backed) | — |
| Start to healthy | < 200 ms | 410-557 ms; 1.1-1.5 s in a later, slower run | 115 ms on an empty database, 29-59 ms on restarts | — |

Not met: p99 under 2 ms for full 532 kB bodies at 5,000 req/s, and under a login flood. The
same generator against a static Go server (`net/http` only, a 532,082-byte body from memory, no
middleware, no logging) on the same network gave p99 3.8, 9.5, 45.5 and 4.6 ms (4 runs) and
p50 0.9-1.2 ms; against the same server with an empty body, p99 21 ms at 5,000 req/s and a
single connection's round trip of 0.61 ms (p50). At 5,000 req/s the full bodies are 2.66 GB/s
copied through the Docker network on 12 threads shared by the generator, the API, Kuma and the
host. The API's own cost is measured in process below (3-16 µs per cached request through every
middleware); the p99 of the load test is the network and the host here. Revalidated requests
(no body) met the target in both runs taken while the host was quieter.

## In-process benchmarks

`go test -bench -benchmem`, `internal/httpapi/bench_test.go`. The full chain is request id,
recovery, access log (JSON to `io.Discard`), CORS, body limit, authentication and the route.

| Benchmark | Before | Response cache | + token cache | Final (quieter host) |
| --------- | ------ | -------------- | ------------- | -------------------- |
| `GetMonitors` (full chain) | 15.8 ms, 9.7 MB, 53,472 allocs | 16.0 µs, 3.6 kB, 47 allocs | 8.4 µs, 1.8 kB, 15 allocs | 3.1 µs, 15 allocs |
| `GetMonitorsParallel` (12 goroutines) | 3.9 ms/op | 6.3 µs/op | — | 1.6 µs/op |
| `GetMonitor` (`/monitors/100`) | 134 µs, 341 allocs | 13.3 µs, 48 allocs | — | 3.4 µs, 16 allocs |
| `GetMonitorsNotModified` (304) | (no ETag: 29.6 ms, full body) | 16.3 µs, 47 allocs | 7.9 µs, 15 allocs | 3.6 µs, 15 allocs |
| `Unauthorized` (token of another key) | 7.0 µs, 44 allocs | 11.5 µs, 44 allocs | — | 6.5 µs, 44 allocs |
| Route alone, `/monitors` | 23.4 ms, 53,417 allocs | 260 ns, 0 allocs | — | 138 ns, 0 allocs |
| Route alone, `/monitors/100` | — | — | — | 220 ns, 1 alloc (net/http) |
| Route alone, 304 | — | — | — | 134 ns, 0 allocs |
| `auth.Tokens.Subject`, cached | — | — | — | 57 ns, 0 allocs |

The CPU frequency of the laptop varied during the day: compare columns measured together
(before / cache / token cache were taken within an hour; the final column later).

Remaining allocations through the full chain are the middleware's: request id (random id and
its hex), two `context.WithValue` and `Request.WithContext`, the recorder, the access log record.

## Optimisations kept, with their measurements

1. **Response cache** (architecture §5). Each route served from State keeps its encoded body
   and a strong ETag per list version; built lazily on the first read of a version, once,
   on a request goroutine. Not built at push time on the session's reader goroutine: uptime and
   average ping change with every heartbeat and most of their versions are never read, and the
   first build of 200 monitors costs about 15 ms (the "Before" column), which must not delay the
   reader. Object lists re-encode only the objects whose pushed JSON changed, and answer a single
   object from its slice of the list body, so one body per list is kept (no per-id copy).
   `If-None-Match` (weak comparison, lists, `*`) answers 304. Headers added: `ETag`,
   `Cache-Control: private, no-cache` (per user; clients revalidate since Kuma pushes changes at
   any time), and a `Content-Length` where large bodies were sent chunked before. Bodies and all
   other headers are byte-identical to phase 3 (compared on all 25 cached routes and error
   cases against the real Kuma).
   Before/after: see the first rows above; load: from saturation at 150-200 req/s to 5,000 req/s
   sustained.
2. **Plain handlers for the cached routes.** huma's per-request work around a handler returning a
   ready `[]byte` measured 1.15 µs and 7 allocations against 0.35 µs and 1 allocation (net/http's)
   for a plain handler. The cached routes are plain `net/http` handlers behind the same
   middleware; they are registered in the same OpenAPI document through a huma API that serves
   nothing (`docsOnly`), so the document is unchanged (`TestOpenAPIDocument`, conformance tests).
3. **Verified-token cache** (`auth.Tokens`). JWT verification was 42% of the CPU profile of a
   revalidated `GET /monitors` through the middleware. Up to 1,024 verified tokens are kept with
   their subject and expiry; a hit checks expiry exactly as a full verification does; invalid
   tokens are never cached; the user is still looked up on every request (deleted users are
   rejected at once); the key is fixed for the life of the cache. Full chain 16.3 µs / 47 allocs
   to 7.9 µs / 15 allocs.
4. **Log lines off the request path** (`cmd/uptime-kuma-api/logwriter.go`). slog's JSON handler
   wrote each access log line to standard error under its lock; one goroutine now writes buffered
   lines in order (back pressure at 1 MiB, drained on exit). Revalidated `GET /monitors` at
   5,000 req/s: p99 8.8 and 8.9 ms before, 5.4 and 5.0 ms after (alternating runs).
5. **Latest heartbeat only.** `State` kept the 100 heartbeats per monitor Kuma sends at login;
   no route reads them. Live heap after login 8 MB to 3 MB; idle RSS 28.9 MB to 22.9 MB.
6. **Start.** The dummy hash for unknown users is a constant at cost 12 instead of a bcrypt at
   every start, and the admin setup (a bcrypt check of `ADMIN_PASSWORD`) runs while the server
   already answers; logins and authenticated requests wait for it (at most 3 s, then 503).
   1.1-1.5 s to 29-59 ms (same session).
7. **Single-flight reads.** Identical concurrent reads that are not served from State share one
   call to Kuma; a read only joins a call sent after every write that had returned
   (read-your-writes), and the call is only canceled when every caller has gone. Not measured
   under load: it bounds the calls to Kuma rather than the latency of one read.
8. **Coalesced monitor list refreshes.** 12 tag writers for 30 s on the real Kuma with 200
   monitors: before, 36 edits, 36 refreshes timed out (`cannot refresh the monitor list`), edit
   p50 14.8 s; after, 145 edits, no warning, edit p50 2.1 s.
9. **Bounded bcrypt.** At most `GOMAXPROCS/4` (3 here) bcrypt operations at once; a login or user
   creation waits up to 3 s for a slot, then 503 with `Retry-After: 1`. Revalidated reads at
   5,000 req/s with 24 connections flooding the login: p50 27 s before (the floods' bcrypt took
   every CPU), p50 0.74-1.85 ms, p99 5.5-19 ms after. The flood itself: about 10 logins/s
   answered 401, the rest 503 after 3 s.

Tried and dropped: nothing else was implemented. A per-id copy of each single-object body was
not built: answering from the list body's slice already allocates nothing.

## Python 2.0 baseline

`medaziz11/uptimekuma_restapi:v2.0.0` on the same network and Kuma, same generator, 64
connections, 20 s after 3 s of warm-up:

| Rate | p50 | p99 |
| ---- | --- | --- |
| 1 req/s (1 connection) | 1.6 s | 2.4 s |
| 5 req/s | 2.2 s | 3.7 s |
| 10 req/s | 19 s | 30 s |
| 20 req/s | 48 s | 83 s |

It sustains about 5 req/s; at 10 req/s the queue grows for the whole run. Its body is the same
532,082 bytes.
