import sqlite3

import bcrypt
from fastapi.testclient import TestClient

from app.main import create_app
from tests.conftest import ADMIN_PASSWORD, login

# Schema and row format written by v1 (tortoise-orm 0.19, passlib bcrypt, naive timestamps).
V1_SCHEMA = """
CREATE TABLE IF NOT EXISTS "users" (
    "id" INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    "username" VARCHAR(20) NOT NULL UNIQUE,
    "password_hash" VARCHAR(200),
    "created_at" TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    "last_visit" TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);
"""


def test_v1_database_keeps_working(settings):
    settings.DB_PATH.parent.mkdir(parents=True)
    db = sqlite3.connect(settings.DB_PATH)
    db.executescript(V1_SCHEMA)
    for username, password in [("admin", "old-admin-password"), ("legacy", "legacy-password")]:
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

    with TestClient(create_app(settings)) as client:
        legacy = login(client, "legacy", "legacy-password")
        me = client.get("/users/me", headers=legacy).json()
        assert me["username"] == "legacy"
        assert me["created_at"].startswith("2023-04-11T10:00:00")
        assert not me["last_visit"].startswith("2023")  # refreshed by the login

        # The admin password now follows ADMIN_PASSWORD.
        admin = login(client, "admin", ADMIN_PASSWORD)
        assert len(client.get("/users", headers=admin).json()) == 2
