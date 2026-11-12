# End-to-end tests

These tests call the API while it is connected to a real Uptime Kuma. They are skipped unless
`E2E_API_URL` and `E2E_ADMIN_PASSWORD` are set, so a plain `pytest` run never needs Docker.

```bash
cp .env.dist .env            # set KUMA_USERNAME, KUMA_PASSWORD, ADMIN_PASSWORD
make e2e                     # starts the stack, creates the Kuma user, runs the tests, stops the stack
```

Step by step:

```bash
docker compose up -d --build --wait
set -a; . ./.env; set +a
python tests/e2e/setup_kuma.py http://127.0.0.1:3001      # first user of a fresh Uptime Kuma
E2E_API_URL=http://127.0.0.1:8000 E2E_ADMIN_PASSWORD="$ADMIN_PASSWORD" pytest tests/e2e
docker compose down -v
```

| Variable             | Description                                                                 |
| -------------------- | --------------------------------------------------------------------------- |
| `E2E_API_URL`        | Base URL of the API under test                                              |
| `E2E_ADMIN_PASSWORD` | Its `ADMIN_PASSWORD`                                                        |
| `E2E_MONITOR_URL`    | URL the test monitor checks, as seen from Uptime Kuma (default `http://api:8000/health`) |

The tests create and delete their own monitors, tags, status pages, ... and leave existing data
alone, but they are meant for a disposable Uptime Kuma, not a production one.

CI runs them on every push against the Uptime Kuma versions listed in
`.github/workflows/ci.yml`.
