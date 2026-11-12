# Uptime-Kuma-Web-API

A REST API wrapper for [Uptime Kuma](https://github.com/louislam/uptime-kuma), built with
[FastAPI](https://fastapi.tiangolo.com/) on top of
[uptime-kuma-api](https://github.com/lucasheld/uptime-kuma-api).

[![CI](https://github.com/MedAziz11/Uptime-Kuma-Web-API/actions/workflows/main.yml/badge.svg)](https://github.com/MedAziz11/Uptime-Kuma-Web-API/actions/workflows/main.yml)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)

## Compatibility

| Component   | Supported versions |
| ----------- | ------------------ |
| Uptime Kuma | 1.21.3 – 1.23.x    |
| Python      | 3.11+              |

`uptime-kuma-api` 1.2.1 officially supports Uptime Kuma 1.21.3 – 1.23.2; later 1.23.x patch releases
are expected to work. Uptime Kuma 2.x is **not** supported: `uptime-kuma-api` has no release for it.

## Features

- Monitors, tags, status pages and incidents, maintenances, notifications, proxies and docker hosts
- Uptime, average ping, certificate and server info
- JWT authentication with a small SQLite user database; user management is admin only
- One shared, self-reconnecting connection to Uptime Kuma
- Interactive Swagger docs at `/docs`, liveness probe at `/health`
- Docker image for amd64 and arm64: [medaziz11/uptimekuma_restapi](https://hub.docker.com/r/medaziz11/uptimekuma_restapi)

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
    image: louislam/uptime-kuma:1
    ports:
      - "3001:3001"
    restart: always
    volumes:
      - uptime-kuma:/app/data

  api:
    container_name: backend
    image: medaziz11/uptimekuma_restapi
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
create the Kuma user named in `.env`. The API connects to Uptime Kuma on the first request, so no
restart is needed afterwards.

Then open <http://localhost:8000/docs> and log in as `admin` with your `ADMIN_PASSWORD`.

## Configuration

| Variable                  | Required | Default           | Description                                                                 |
| ------------------------- | -------- | ----------------- | --------------------------------------------------------------------------- |
| `KUMA_SERVER`             | yes      |                   | URL of the Uptime Kuma instance, e.g. `https://uptime.example.com`           |
| `KUMA_USERNAME`           | yes      |                   | Uptime Kuma user                                                            |
| `KUMA_PASSWORD`           | yes      |                   | Uptime Kuma password                                                        |
| `ADMIN_PASSWORD`          | yes      |                   | Password of the API `admin` user. Changing it updates the user at startup.  |
| `SECRET_KEY`              | no       | generated         | JWT signing key. When unset, one is generated and stored next to the database. |
| `ACCESS_TOKEN_EXPIRATION` | no       | `11520` (8 days)  | Access token lifetime, in minutes                                           |
| `KUMA_TIMEOUT`            | no       | `10`              | Seconds to wait for Uptime Kuma to answer                                   |
| `KUMA_SSL_VERIFY`         | no       | `true`            | Set to `false` to accept a self-signed Uptime Kuma certificate              |
| `BACKEND_CORS_ORIGINS`    | no       | `[]`              | JSON list of origins allowed by CORS                                        |
| `DB_PATH`                 | no       | `db/db.sqlite3`   | SQLite file holding the API users (`/db/db.sqlite3` in the Docker image)    |
| `LOG_LEVEL`               | no       | `INFO`            | Log level                                                                   |

The Uptime Kuma account must not have two-factor authentication enabled.

## Authentication

```bash
TOKEN=$(curl -s -X POST \
  -H 'Content-Type: application/x-www-form-urlencoded' \
  --data 'username=admin&password=<ADMIN_PASSWORD>' \
  http://127.0.0.1:8000/login/access-token | jq -r ".access_token")

curl -H "Authorization: Bearer ${TOKEN}" http://127.0.0.1:8000/monitors
```

Every endpoint except `/login/access-token`, `/health` and the docs requires a token.

The `admin` user can create, list and delete other users (`/users`). Other users have full access to
the Uptime Kuma endpoints but cannot manage users.

## Endpoints

| Prefix           | Operations                                                                 |
| ---------------- | -------------------------------------------------------------------------- |
| `/monitors`      | list, get, create, update, delete, pause, resume, heartbeats, add/remove tag |
| `/tags`          | list, get, create, update, delete                                          |
| `/statuspages`   | list, get, create, save, delete, post/unpin incident                       |
| `/maintenance`   | list, get, create, update, delete, pause, resume, get/set monitors         |
| `/notifications` | list, get, create, update, delete, test                                    |
| `/proxies`       | list, get, create, update, delete                                          |
| `/docker_hosts`  | list, get, create, update, delete, test                                    |
| `/users`         | create, list, get, delete (admin), `/users/me`                             |
| `/info`, `/uptime`, `/ping`, `/cert_info` | read-only server and monitor statistics          |
| `/database`      | size, shrink                                                               |
| `/settings`      | import a backup                                                            |

The full, always up-to-date reference is the Swagger UI at `/docs` (OpenAPI schema at `/openapi.json`).

### Errors

Errors are returned as `{"detail": "..."}`.

| Status | Meaning                                                        |
| ------ | -------------------------------------------------------------- |
| 400    | Uptime Kuma rejected the request                               |
| 401    | Missing, invalid or expired token; wrong username or password  |
| 403    | Admin privileges required                                      |
| 404    | The monitor, tag, status page... does not exist                |
| 422    | Invalid request body or parameters                             |
| 503    | Uptime Kuma is unreachable or refused the configured credentials |
| 504    | Uptime Kuma did not answer in time                             |

## Development

```bash
python -m venv .venv
source .venv/bin/activate          # Windows: .venv\Scripts\activate
pip install -r requirements-dev.txt

make run      # uvicorn with auto-reload (reads .env)
make lint     # ruff
make test     # pytest (Uptime Kuma is mocked, no server needed)
make up       # full stack with docker compose
make e2e      # curl scripts against the running stack
```

## Upgrading from 1.x

See the [changelog](CHANGELOG.md) for the breaking changes in 2.0.

## License

[MIT](LICENSE)
