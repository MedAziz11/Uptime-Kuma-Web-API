# Contributing

This branch is 3.x (Go, Uptime Kuma 2.x). The 2.x line (Python, Uptime Kuma 1.x) lives on the
branch `2.x` and only takes security fixes.

## Setup

Go 1.27 (see `go.mod`); Docker for the image and the acceptance tests; Python 3.11 or newer for
the acceptance tests only (`pip install -r tests/e2e/requirements.txt`).

## Everyday commands

| Command      | What it does                                                                  |
| ------------ | ----------------------------------------------------------------------------- |
| `make fmt`   | `gofmt -w .`                                                                  |
| `make vet`   | `go vet ./...`                                                                |
| `make lint`  | golangci-lint (`.golangci.yml`) and govulncheck, at the versions CI uses      |
| `make test`  | Unit tests with the race detector and coverage; no Uptime Kuma or Docker needed |
| `make bench` | In-process benchmarks                                                         |
| `make build` | Static binary `./uptime-kuma-api`; `make run` starts it (needs the variables of `.env.dist`) |
| `make docker`| The image (`VERSION=...` sets the version it reports)                         |
| `make e2e`   | Acceptance tests against a real Uptime Kuma in Docker ([details](tests/e2e/README.md)) |

## Project layout

```
cmd/uptime-kuma-api/   main: configuration, wiring, graceful shutdown, healthcheck subcommand
internal/config/       environment variables (names and defaults of 2.0)
internal/kuma/sio/     Engine.IO / Socket.IO client over WebSocket
internal/kuma/         Uptime Kuma session, pushed state, typed commands, monitor defaults
internal/auth/         bcrypt, JWT (HS256), secret key file
internal/store/        users in SQLite (the 1.x/2.0 database, unchanged)
internal/httpapi/      routes, request/response types, middleware, response cache, OpenAPI
internal/version/      the version, set at build time
tools/loadtest/        load generator (docs/performance.md)
tools/kuma-capture/    scripts that record Uptime Kuma's frames into internal/kuma/testdata
tests/e2e/             acceptance suite: the API in front of a real Uptime Kuma, HTTP only
```

The design is in [docs/architecture.md](docs/architecture.md); what Uptime Kuma 2.x does is in
[docs/kuma-2x-notes.md](docs/kuma-2x-notes.md).

## Tests

- **Unit tests** next to the code. `internal/kuma` runs against an in-process fake Uptime Kuma that
  speaks Socket.IO over a real WebSocket; payloads come from recordings of a real Uptime Kuma
  (`internal/kuma/testdata`). `internal/httpapi` replays requests recorded against 2.0
  (conformance tests). Tests against a real Uptime Kuma are skipped unless their environment
  variables are set.
- **Acceptance tests** (`tests/e2e`) run every route against real Uptime Kuma versions. Add a case
  there whenever a route is added: Uptime Kuma's answers are not always what its source suggests.

## Adding a route

1. Record what Uptime Kuma sends for the command (`tools/kuma-capture`) instead of guessing.
2. Add the command to `internal/kuma` and the route to `internal/httpapi` with request and
   response types; the OpenAPI document is generated from them.
3. Every route requires a token unless it is listed in `publicPaths`.
4. Add unit tests and an acceptance test, and update `CHANGELOG.md` and the README table.

## Pull requests

- Branch from `develop` (or `main` for hotfixes).
- CI must pass: gofmt, go vet, unit tests with the race detector, golangci-lint, govulncheck, the
  acceptance tests on three Uptime Kuma versions, and the multi-arch image build.
- Add a line under "Unreleased" in `CHANGELOG.md` for user-visible changes.
- No new Go dependency without a line in the table of architecture.md §10.

## Releasing (maintainers)

1. Choose the version (semantic versioning; breaking changes bump the major). A release candidate
   is `X.Y.Z-rc.N`.
2. In `CHANGELOG.md`, rename "Unreleased" to `[X.Y.Z]` with today's date.
3. Write `docs/releases/vX.Y.Z.md` (it becomes the GitHub release body; generated notes otherwise).
4. Merge to `main`, then tag and push:

   ```bash
   git tag vX.Y.Z
   git push origin vX.Y.Z
   ```

The `Release` workflow (tags `v3.*`) checks that the tag is `v3.X.Y` or `v3.X.Y-rc.N` and has a
`CHANGELOG.md` section, runs the tests, builds the image for amd64 and arm64 with the version set
from the tag, pushes it with an SBOM and provenance (`vX.Y.Z`, `vX.Y`, `vX`, `latest`; a release
candidate only `vX.Y.Z-rc.N`) and creates the GitHub release (a pre-release for `-rc.N`).

It needs the repository secrets `DOCKER_USERNAME` and `DOCKER_PASSWORD` (a Docker Hub access token).
The image name can be overridden with the repository variables `DOCKERHUB_REPO` and
`DOCKERHUB_IMAGE`.
