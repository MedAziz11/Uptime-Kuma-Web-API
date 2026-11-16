"""Writes the user databases that the Go upgrade tests open.

Run from the root of a checkout of tag v2.0.0 (the Python sources are no
longer on this branch), with its requirements.txt installed and this script
copied there:

    python internal/store/testdata/gen_fixtures.py

Outputs, next to this script:

v1/db.sqlite3      A 1.x database: the schema and row format of tortoise-orm 0.19
                   with passlib bcrypt hashes and naive timestamps, built exactly
                   as tests/test_upgrade.py builds it.
v2.0/db.sqlite3    A database written by the 2.0 application itself (app.main),
                   through its HTTP routes.
v2.0/secret_key    The signing key 2.0 generated next to that database.
users.json         Users, passwords and a long-lived token minted by 2.0's
                   app.security.create_access_token with that key.

The passwords are test values. Hashes use bcrypt's default cost (12), as 2.0
does in production.
"""

import json
import shutil
import sqlite3
import sys
from datetime import timedelta
from pathlib import Path

HERE = Path(__file__).resolve().parent
ROOT = HERE.parents[2]
sys.path.insert(0, str(ROOT))

import bcrypt  # noqa: E402
from fastapi.testclient import TestClient  # noqa: E402

from app.config import Settings  # noqa: E402
from app.main import create_app  # noqa: E402
from app.security import create_access_token  # noqa: E402

V1_SCHEMA = """
CREATE TABLE IF NOT EXISTS "users" (
    "id" INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    "username" VARCHAR(20) NOT NULL UNIQUE,
    "password_hash" VARCHAR(200),
    "created_at" TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    "last_visit" TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);
"""

V1_USERS = [("admin", "old-admin-password"), ("legacy", "legacy-password")]

V2_ADMIN_PASSWORD = "v2-admin-password"
V2_USERS = [("alice", "alice-password"), ("bob", "bob-password"), ("carol", "pässwörd-密码")]


def fresh_dir(path: Path) -> Path:
    shutil.rmtree(path, ignore_errors=True)
    path.mkdir(parents=True)
    return path


def write_v1(directory: Path) -> list[dict]:
    db = sqlite3.connect(directory / "db.sqlite3")
    db.executescript(V1_SCHEMA)
    for username, password in V1_USERS:
        db.execute(
            "INSERT INTO users (username, password_hash, created_at, last_visit) "
            "VALUES (?, ?, ?, ?)",
            (
                username,
                bcrypt.hashpw(password.encode(), bcrypt.gensalt(prefix=b"2b")).decode(),
                "2023-04-11 10:00:00.123456",
                "2023-04-11 10:00:00.123456",
            ),
        )
    db.commit()
    db.close()
    return [{"username": u, "password": p} for u, p in V1_USERS]


def write_v2(directory: Path) -> dict:
    settings = Settings(
        _env_file=None,
        KUMA_SERVER="http://kuma.invalid:3001",
        KUMA_USERNAME="kuma-user",
        KUMA_PASSWORD="kuma-password",
        ADMIN_PASSWORD=V2_ADMIN_PASSWORD,
        DB_PATH=directory / "db.sqlite3",
    )
    with TestClient(create_app(settings)) as client:
        resp = client.post(
            "/login/access-token", data={"username": "admin", "password": V2_ADMIN_PASSWORD}
        )
        resp.raise_for_status()
        admin = {"Authorization": f"Bearer {resp.json()['access_token']}"}
        for username, password in V2_USERS:
            resp = client.post(
                "/users", json={"username": username, "password": password}, headers=admin
            )
            assert resp.status_code == 201, resp.text
        # A deleted user leaves a gap in the ids, as on a real volume.
        assert client.delete("/users/bob", headers=admin).status_code == 200
        # alice has logged in, so her last_visit differs from created_at.
        resp = client.post(
            "/login/access-token", data={"username": "alice", "password": "alice-password"}
        )
        resp.raise_for_status()
        listed = client.get("/users", headers=admin).json()

    key = (directory / "secret_key").read_text(encoding="utf-8")
    users = [{"username": "admin", "password": V2_ADMIN_PASSWORD}] + [
        {"username": u, "password": p} for u, p in V2_USERS if u != "bob"
    ]
    return {
        "users": users,
        "deleted": ["bob"],
        # What 2.0 answers on GET /users for this database.
        "listed": listed,
        "alice_token": create_access_token("alice", key, timedelta(days=365 * 100)),
    }


def main() -> None:
    v1 = write_v1(fresh_dir(HERE / "v1"))
    v2 = write_v2(fresh_dir(HERE / "v2.0"))
    for leftover in (HERE / "v2.0").glob("db.sqlite3-*"):
        raise SystemExit(f"unexpected file left by SQLite: {leftover}")
    out = {"v1": {"users": v1}, "v2.0": v2}
    (HERE / "users.json").write_text(json.dumps(out, indent=2, ensure_ascii=False) + "\n", "utf-8")


if __name__ == "__main__":
    main()
