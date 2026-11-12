# Changelog

All notable changes to this project are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and the project uses
[Semantic Versioning](https://semver.org/).

## [2.0.0] - 2026-11-12

Upgrade guide: [docs/releases/v2.0.0.md](docs/releases/v2.0.0.md).

### Breaking changes

- **`POST /users` requires an admin token.** In 1.x anyone could create an account without
  authenticating and then control Uptime Kuma. User management (`POST`, `GET`, `DELETE /users`) is
  now reserved to the `admin` user. `POST /users` returns `201`, and `409` for a duplicate username.
- **Existing tokens are invalid.** Tokens no longer embed the Uptime Kuma session token (a JWT is
  signed, not encrypted, so 1.x exposed it to every client). Log in again after upgrading.
- **Status codes.** Wrong credentials and invalid or expired tokens return `401` (were `400`/`403`).
  Unknown resources return `404` on every route. Uptime Kuma being unreachable returns `503`, a
  timeout `504`. Unexpected errors no longer return the exception text.
- **Error bodies** are always `{"detail": "<message>"}` (some routes returned
  `{"detail": {"message": ...}}`).
- **Uptime Kuma 1.21.3 – 1.23.x** is required (`uptime-kuma-api` 0.13.0 → 1.2.1).
- **`ADMIN_PASSWORD` is the source of truth**: changing it updates the `admin` user at startup.
  The `admin` user can no longer be deleted.
- **New usernames** must be 3–20 characters of `A-Z a-z 0-9 _ . -`; new passwords 8–72 bytes.
- **Create/update bodies** forward only the fields you send; everything else keeps the Uptime Kuma
  default. 1.x sent its own defaults (e.g. `port: 53` on every monitor).
- **Timestamps** in `/users` responses are timezone-aware (UTC).
- **Docker tags**: `latest` is now the latest release; the tip of `main` is published as `edge`.
  Releases are also tagged `vX.Y` and `vX`.
- For developers: `app` is a package; run it with `uvicorn app.main:create_app --factory`.

### Fixed

- A new Socket.IO connection to Uptime Kuma was opened on every request and never closed. There is
  now one shared connection that reconnects and logs in again when needed.
- Blocking Uptime Kuma calls no longer stall the event loop.
- The API starts even when Uptime Kuma is down or its user does not exist yet.
- Without `SECRET_KEY`, tokens were invalidated at every restart. A key is now generated once and
  stored next to the database.
- `GET /statuspages` failed when a status page had a domain name (validated as a URL).
- The default maintenance `dateRange` was frozen at the date the server started.
- Login no longer logs at `FATAL` level on every call.
- `/cert_info`, `/ping` and `/uptime` return `{}` when Uptime Kuma has no data (e.g. no HTTPS
  monitor) instead of failing after a timeout.
- Unknown ids return `404` even where Uptime Kuma answers with a raw JavaScript or SQL error.
- A deleted user's token stops working immediately.

### Added

- `PATCH /tags/{id}`, `GET /users/me`, `GET /health`.
- `/notifications`, `/proxies` and `/docker_hosts` endpoints.
- Monitor options added in Uptime Kuma 1.22/1.23 (groups, JSON query, Kafka, gRPC, ...) and
  cron-based maintenances.
- Settings: `KUMA_TIMEOUT`, `KUMA_SSL_VERIFY`, `DB_PATH`, `LOG_LEVEL`; `BACKEND_CORS_ORIGINS` is now
  applied.
- Tests: unit tests (100 % coverage, CI fails under 95 %), an integration test of the real
  `uptime-kuma-api` client against a stand-in Socket.IO server, and end-to-end tests against
  Uptime Kuma 1.21.3, 1.23.2 and the latest 1.x.
- CI: lint, unit tests on Python 3.11–3.13, end-to-end tests, multi-arch image build;
  Dependabot; a tag-driven release workflow (Docker image + GitHub release).
- Docker health check; `KUMA_VERSION` selects the Uptime Kuma image in `docker-compose.yml`.
- `CONTRIBUTING.md`, `SECURITY.md`, issue and pull request templates, release notes.
- MIT license.

### Changed

- Python 3.13 image, FastAPI 0.162, pydantic 2. `passlib` (unmaintained) replaced by `bcrypt`;
  existing password hashes keep working. `gunicorn` replaced by `uvicorn`.
- The existing `/db` volume and user database are reused as-is.

### Removed

- The manual release workflow (its action reference was invalid) and the automatic tag-on-merge
  workflow, both replaced by `release.yml`.
- `diff.txt`, the outdated endpoint screenshots, and the curl test scripts (replaced by
  `tests/e2e`).

[2.0.0]: https://github.com/MedAziz11/Uptime-Kuma-Web-API/compare/v1.2.2...v2.0.0
