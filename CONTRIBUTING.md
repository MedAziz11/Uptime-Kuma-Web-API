# Contributing

## Setup

Python 3.11 or newer.

```bash
python -m venv .venv
source .venv/bin/activate          # Windows: .venv\Scripts\activate
pip install -r requirements-dev.txt
pre-commit install                 # optional: lint and format on commit
```

## Everyday commands

| Command         | What it does                                                               |
| --------------- | -------------------------------------------------------------------------- |
| `make run`      | Run the API with auto-reload (reads `.env`)                                |
| `make lint`     | `ruff check` and `ruff format --check`                                     |
| `make format`   | Fix lint findings and format                                               |
| `make test`     | Unit and integration tests; no Uptime Kuma or Docker needed                |
| `make coverage` | Same, with the coverage report (CI fails under 95 %)                       |
| `make e2e`      | End-to-end tests against a real Uptime Kuma in Docker ([details](tests/e2e/README.md)) |

## Project layout

```
app/
  main.py        application factory, lifespan, router registration
  config.py      settings (environment variables)
  kuma.py        KumaClient: the shared, self-reconnecting Uptime Kuma connection
  errors.py      Uptime Kuma errors -> HTTP status codes
  deps.py        authentication and other dependencies
  security.py    password hashing and JWT
  database.py    SQLite user database
  routers/       one module per URL prefix
  schemas/       request/response models
tests/
  test_*.py      unit tests (Uptime Kuma is mocked)
  test_kuma_integration.py   real uptime-kuma-api client against a stand-in Socket.IO server
  e2e/           the API in front of a real Uptime Kuma
```

## Tests

Three layers, because each one misses things the others catch:

- **Route tests** (`test_kuma_routes.py`, `test_auth_users.py`, ...) replace `KumaClient` with a
  fake. They check validation, authentication, what is forwarded to Uptime Kuma and how errors are
  mapped.
- **Client tests** (`test_kuma_client.py`, `test_kuma_integration.py`) check the connection
  handling: lazy connect, reconnect, re-login, timeouts.
- **End-to-end tests** (`tests/e2e`) run every endpoint against real Uptime Kuma versions. Add a
  case there whenever an endpoint is added: Uptime Kuma's answers are not always what
  the library documents.

## Adding an endpoint

1. Add the route to the matching module in `app/routers/` as a plain `def` (not `async def`):
   `KumaClient` blocks, and FastAPI runs `def` routes in a thread pool.
2. Call the library through the injected client: `kuma.some_method(...)`. Any public method of
   `UptimeKumaApi` is available.
3. Do not catch Uptime Kuma exceptions in the route; `app/errors.py` maps them.
4. A new router must be registered in `app/main.py` inside the authenticated loop.
   `tests/test_app.py` fails if a route answers without a token.
5. Add a route test and an end-to-end test, and update `CHANGELOG.md` and the README table.

## Pull requests

- Branch from `develop` (or `main` for hotfixes).
- CI must pass: lint, unit tests on Python 3.11–3.13, end-to-end tests on three Uptime Kuma versions,
  and the multi-arch image build.
- Add a line under "Unreleased" in `CHANGELOG.md` for user-visible changes.

## Releasing (maintainers)

1. Choose the version (semantic versioning; breaking changes bump the major).
2. Update the version in `app/__init__.py` and `pyproject.toml`.
3. In `CHANGELOG.md`, rename "Unreleased" to the version with today's date.
4. Write `docs/releases/vX.Y.Z.md` (it becomes the GitHub release body).
   `tests/test_app.py` checks that these four places agree.
5. Merge to `main`, then tag and push:

   ```bash
   git tag vX.Y.Z
   git push origin vX.Y.Z
   ```

The `Release` workflow then checks the tag against the code, runs the tests, publishes the Docker
image (`vX.Y.Z`, `vX.Y`, `vX`, `latest`) and creates the GitHub release.

It needs the repository secrets `DOCKER_USERNAME` and `DOCKER_PASSWORD` (a Docker Hub access token).
The image name can be overridden with the repository variables `DOCKERHUB_REPO` and
`DOCKERHUB_IMAGE`.
