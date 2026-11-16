# Uptime-Kuma-Web-API

A REST API for [Uptime Kuma](https://github.com/louislam/uptime-kuma) 2.x. Uptime Kuma is managed
over Socket.IO only; this API keeps one connection to it and offers the same operations as JSON
over HTTP, with its own users and JWT authentication.

3.x is written in Go and talks to Uptime Kuma 2.x. It keeps the routes, bodies, status codes,
environment variables and `/db` volume of 2.0, so 2.0 clients and deployments keep working; the
differences are listed in the [migration guide](docs/migration-2-to-3.md).

[![CI](https://github.com/MedAziz11/Uptime-Kuma-Web-API/actions/workflows/ci.yml/badge.svg)](https://github.com/MedAziz11/Uptime-Kuma-Web-API/actions/workflows/ci.yml)
[![Docker pulls](https://img.shields.io/docker/pulls/medaziz11/uptimekuma_restapi)](https://hub.docker.com/r/medaziz11/uptimekuma_restapi)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)

## Compatibility

| API | Uptime Kuma | Status |
| --- | ----------- | ------ |
| 3.x (this branch, Go) | 2.x | supported |
| 2.x (branch [`2.x`](https://github.com/MedAziz11/Uptime-Kuma-Web-API/tree/2.x), Python) | 1.21.3 – 1.23.x | security fixes only |
| 1.x | 1.x | not supported |

The acceptance suite passes against Uptime Kuma 2.5.11. CI also runs it against 2.0.0 and the
moving `2` tag. On Uptime Kuma 2.0.0, creating a monitor and reading the incident history fail
(see [Known limitations](docs/migration-2-to-3.md#known-limitations)); use a current 2.x release.

## Quick start

Create a `.env` file (see [.env.dist](.env.dist)):

```env
KUMA_USERNAME=your-kuma-user
KUMA_PASSWORD=your-kuma-password
ADMIN_PASSWORD=choose-a-strong-password
```

and a `docker-compose.yml`:

```yaml
services:
  kuma:
    container_name: uptime-kuma
    image: louislam/uptime-kuma:2
    environment:
      - UPTIME_KUMA_DB_TYPE=sqlite   # no database setup page on the first start
    ports:
      - "3001:3001"
    restart: always
    volumes:
      - uptime-kuma:/app/data

  api:
    image: medaziz11/uptimekuma_restapi:3
    volumes:
      - api:/db
    restart: always
    environment:
      - KUMA_SERVER=http://kuma:3001
      - KUMA_USERNAME=${KUMA_USERNAME}
      - KUMA_PASSWORD=${KUMA_PASSWORD}
      - ADMIN_PASSWORD=${ADMIN_PASSWORD}
    depends_on:
      - kuma
    ports:
      - "8000:8000"

volumes:
  uptime-kuma:
  api:
```

Start it with `docker compose up -d`. On a fresh Uptime Kuma, open <http://localhost:3001> once to
create the Kuma user named in `.env` (or run `python tests/e2e/setup_kuma.py`, see
[tests/e2e](tests/e2e/README.md)). The API connects to Uptime Kuma on the first request, so no
restart is needed afterwards.

Then open <http://localhost:8000/docs> and log in as `admin` with your `ADMIN_PASSWORD`.

The [docker-compose.yml](docker-compose.yml) of this repository builds the API from source instead.

### Image

`medaziz11/uptimekuma_restapi` for `linux/amd64` and `linux/arm64`: one static binary on
`gcr.io/distroless/static-debian12` (no shell), 6.5 MB compressed. It runs as uid 100, gid 101
(the user of the 2.0 image), keeps its users in the `/db` volume, listens on port 8000 and has a
`HEALTHCHECK` (`/uptime-kuma-api healthcheck`).

| Tag                          | Content                       |
| ---------------------------- | ----------------------------- |
| `latest`                     | the latest release            |
| `3.0.0`, `3.0`, `3`          | a specific release / line     |
| `3.0.0-rc.1`                 | a release candidate (only this tag) |
| `v2.0.0`, `v2.0`, `v2`       | the 2.x line (Uptime Kuma 1.x) |
| `edge`                       | the tip of `main`             |
| `dev`                        | the tip of `develop`          |

## Configuration

Environment variables (`internal/config`). Names and defaults are those of 2.0.

| Variable                  | Required | Default           | Description                                                                 |
| ------------------------- | -------- | ----------------- | --------------------------------------------------------------------------- |
| `KUMA_SERVER`             | yes      |                   | URL of the Uptime Kuma instance, e.g. `https://uptime.example.com`           |
| `KUMA_USERNAME`           | yes      |                   | Uptime Kuma user                                                            |
| `KUMA_PASSWORD`           | yes      |                   | Uptime Kuma password                                                        |
| `ADMIN_PASSWORD`          | yes      |                   | Password of the API `admin` user (at most 72 bytes). Changing it updates the user at startup. |
| `SECRET_KEY`              | no       | generated         | JWT signing key. When unset, one is generated and stored next to the database (`secret_key`, mode 0600). |
| `ACCESS_TOKEN_EXPIRATION` | no       | `11520` (8 days)  | Access token lifetime, in minutes                                           |
| `KUMA_TIMEOUT`            | no       | `10`              | Seconds to wait for Uptime Kuma to answer                                   |
| `KUMA_SSL_VERIFY`         | no       | `true`            | Set to `false` to accept a self-signed Uptime Kuma certificate              |
| `BACKEND_CORS_ORIGINS`    | no       | `[]`              | JSON list of origins allowed by CORS                                        |
| `DB_PATH`                 | no       | `db/db.sqlite3`   | SQLite file holding the API users (`/db/db.sqlite3` in the Docker image)    |
| `LOG_LEVEL`               | no       | `INFO`            | `DEBUG`, `INFO`, `WARNING`, `ERROR` or `CRITICAL`                           |
| `LISTEN_ADDR`             | no       | `:8000`           | Address the HTTP server listens on (new in 3.0)                             |

A blank secret counts as unset. An invalid value stops the API at start with exit status 2 and a
message naming every variable at fault. The Uptime Kuma account must not have two-factor
authentication enabled. Logs are JSON lines on standard error.

## Authentication

```bash
TOKEN=$(curl -s -X POST \
  -H 'Content-Type: application/x-www-form-urlencoded' \
  --data 'username=admin&password=<ADMIN_PASSWORD>' \
  http://127.0.0.1:8000/login/access-token | jq -r ".access_token")

curl -H "Authorization: Bearer ${TOKEN}" http://127.0.0.1:8000/monitors
```

Every route except `/login/access-token`, `/health`, `/docs`, `/redoc` and `/openapi.json`
requires a token (HS256 JWT). Tokens issued by 2.0 stay valid when the `/db` volume (or
`SECRET_KEY`) is kept.

The `admin` user can create, list and delete other users (`/users`). Other users have full access to
the Uptime Kuma routes but cannot manage users. A deleted user's token is rejected at once.

## Routes

| Prefix                       | Operations                                                                 |
| ---------------------------- | -------------------------------------------------------------------------- |
| `/monitors`                  | list, get, create, update, delete, pause, resume, heartbeats (`beats`), add/remove tag; new: `chart`, `important_beats` |
| `/tags`                      | list, get, create, update, delete                                          |
| `/statuspages`               | list, get, create, save, delete, post/unpin incident; new: incident history, edit, delete, resolve (`/statuspages/{slug}/incidents`) |
| `/maintenance`               | list, get, create, update, delete, pause, resume, get/set monitors         |
| `/notifications`             | list, get, create, update, delete, test                                    |
| `/proxies`                   | list, get, create, update, delete                                          |
| `/docker_hosts`              | list, get, create, update, delete, test                                    |
| `/api_keys` (new)            | list, get, create, enable, disable, delete                                 |
| `/remote_browsers` (new)     | list, get, create, update, delete, test                                    |
| `/push_examples/{language}` (new) | example code for a push monitor                                       |
| `/users`                     | create, list, get, delete (admin), `/users/me`                             |
| `/info`, `/uptime`, `/ping`, `/cert_info` | read-only server and monitor statistics                       |
| `/database`                  | size, shrink                                                               |
| `/settings/upload_backup`    | always `410 Gone`: Uptime Kuma 2 has no backup import                      |

The reference is the documentation page at `/docs` (OpenAPI 3.1 document at `/openapi.json`),
generated from the Go types.

Lists Uptime Kuma pushes (monitors, notifications, proxies, docker hosts, maintenances, status
pages, API keys, remote browsers, uptime, ping, certificate and server info) are answered from
memory without a call to Uptime Kuma, with a strong `ETag`; `If-None-Match` answers `304`. A write
returns once a following read shows it.

### Errors

Errors are returned as `{"detail": "..."}` (422 as `{"detail": [...]}` in FastAPI's shape).

| Status | Meaning                                                        |
| ------ | -------------------------------------------------------------- |
| 400    | Uptime Kuma rejected the request                               |
| 401    | Missing, invalid or expired token; wrong username or password  |
| 403    | Admin privileges required                                      |
| 404    | The monitor, tag, status page... does not exist                |
| 409    | The username exists                                            |
| 410    | The route no longer works with Uptime Kuma 2 (backup import)   |
| 422    | Invalid request body or parameters                             |
| 503    | Uptime Kuma is unreachable or refused the configured credentials; too many logins at once or the API is starting (with `Retry-After`) |
| 504    | Uptime Kuma did not answer in time                             |

## Performance

Measured on a laptop (Docker Desktop, 12 threads shared by the load generator, the API, Uptime
Kuma and the host) against Uptime Kuma 2.5.11 with 200 monitors; `GET /monitors` answers
532,082 bytes. Details and all runs: [docs/performance.md](docs/performance.md).

| Measure | Target | 3.0 | 2.0 (Python) |
| ------- | ------ | --- | ------------ |
| `GET /monitors`, p99 at 5,000 req/s, 64 connections, full body | < 2 ms | **not met**: 2.8, 6.4, 5.9, 17.1 ms (4 runs) | saturates at about 5 req/s (p50 2.2 s) |
| same, revalidated (`304`) | < 2 ms | 1.44, 1.45 ms | — |
| same, revalidated, during a login flood | < 2 ms | **not met**: 5.5 to 19 ms | — |
| Cost of a cached `GET /monitors` in process (all middleware) | — | 3.1 µs, 15 allocations | — |
| Idle resident memory | < 25 MB | 22.9 MB | — |
| Start to healthy | < 200 ms | 115 ms (empty database), 29–59 ms (restarts) | — |
| Image size, compressed | < 20 MB | 6.5 MB (`linux/amd64`) | — |

The full-body p99 is bound by copying 2.66 GB/s through the Docker network on a shared host: a
plain `net/http` server sending the same body from memory measured p99 3.8 to 45.5 ms on the same
setup.

## Development

Go 1.27 (see `go.mod`). Docker for the image and the acceptance tests.

```bash
make fmt vet test   # gofmt, go vet, unit tests with the race detector
make lint           # golangci-lint and govulncheck (versions pinned in the Makefile)
make bench          # in-process benchmarks
make build          # static binary ./uptime-kuma-api (VERSION=... sets the version)
make docker         # the image
make e2e            # acceptance tests against a real Uptime Kuma in Docker
make up             # full stack with docker compose
```

`uptime-kuma-api -version` prints the version. See [CONTRIBUTING.md](CONTRIBUTING.md) for the
project layout and how to release, [docs/architecture.md](docs/architecture.md) for the design,
[docs/kuma-2x-notes.md](docs/kuma-2x-notes.md) for what was observed on Uptime Kuma 2.x, and
[SECURITY.md](SECURITY.md) for reporting vulnerabilities and deployment notes.

## Upgrading

- From 2.0: [docs/migration-2-to-3.md](docs/migration-2-to-3.md). Swap the image tag and keep the
  environment and the `/db` volume; Uptime Kuma must be 2.x.
- From 1.x: upgrade to 2.0 first, see the [2.0.0 release notes](docs/releases/v2.0.0.md).

The [changelog](CHANGELOG.md) has the full list of changes.

## License

[MIT](LICENSE)
