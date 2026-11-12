import os
import stat

import pytest
from pydantic import ValidationError

from app.config import Settings

REQUIRED = {
    "KUMA_SERVER": "http://kuma:3001",
    "KUMA_USERNAME": "kuma-user",
    "KUMA_PASSWORD": "kuma-password",
    "ADMIN_PASSWORD": "admin-password",
}


def make(tmp_path, **overrides) -> Settings:
    values = {**REQUIRED, "DB_PATH": tmp_path / "db" / "db.sqlite3", **overrides}
    return Settings(_env_file=None, **values)


def test_defaults(tmp_path):
    settings = make(tmp_path)
    assert settings.ACCESS_TOKEN_EXPIRATION == 60 * 24 * 8
    assert settings.KUMA_TIMEOUT == 10
    assert settings.KUMA_SSL_VERIFY is True
    assert settings.BACKEND_CORS_ORIGINS == []
    assert settings.LOG_LEVEL == "INFO"
    assert settings.SECRET_KEY is None


def test_reads_the_environment(monkeypatch, tmp_path):
    for name, value in REQUIRED.items():
        monkeypatch.setenv(name, value)
    monkeypatch.setenv("ACCESS_TOKEN_EXPIRATION", "5")
    monkeypatch.setenv("BACKEND_CORS_ORIGINS", '["https://a.example", "https://b.example"]')
    monkeypatch.setenv("KUMA_SSL_VERIFY", "false")
    monkeypatch.setenv("UNRELATED_VARIABLE", "ignored")
    monkeypatch.chdir(tmp_path)  # no .env file here

    settings = Settings()
    assert settings.KUMA_USERNAME == "kuma-user"
    assert settings.ACCESS_TOKEN_EXPIRATION == 5
    assert settings.BACKEND_CORS_ORIGINS == ["https://a.example", "https://b.example"]
    assert settings.KUMA_SSL_VERIFY is False


@pytest.mark.parametrize("missing", sorted(REQUIRED))
def test_required_settings(monkeypatch, tmp_path, missing):
    for name in REQUIRED:
        monkeypatch.delenv(name, raising=False)
    values = {name: value for name, value in REQUIRED.items() if name != missing}
    with pytest.raises(ValidationError, match=missing):
        Settings(_env_file=None, **values)


@pytest.mark.parametrize("name", ["KUMA_PASSWORD", "ADMIN_PASSWORD"])
def test_blank_passwords_are_rejected(tmp_path, name):
    with pytest.raises(ValidationError, match=name):
        make(tmp_path, **{name: "   "})


@pytest.mark.parametrize(
    "overrides",
    [{"ACCESS_TOKEN_EXPIRATION": 0}, {"KUMA_TIMEOUT": 0}, {"LOG_LEVEL": "LOUD"}],
)
def test_invalid_values(tmp_path, overrides):
    with pytest.raises(ValidationError):
        make(tmp_path, **overrides)


def test_values_are_normalized(tmp_path):
    settings = make(tmp_path, KUMA_SERVER="https://kuma.example.com//", LOG_LEVEL="debug")
    assert settings.KUMA_SERVER == "https://kuma.example.com"
    assert settings.LOG_LEVEL == "DEBUG"


def test_secrets_are_not_shown_in_repr(tmp_path):
    settings = make(tmp_path, SECRET_KEY="super-secret-key")
    text = repr(settings)
    for secret in ("kuma-password", "admin-password", "super-secret-key"):
        assert secret not in text


def test_explicit_secret_key_is_used_and_not_stored(tmp_path):
    settings = make(tmp_path, SECRET_KEY="explicit-key")
    assert settings.resolve_secret_key() == "explicit-key"
    assert not (tmp_path / "db").exists()


def test_secret_key_is_generated_once_and_persisted(tmp_path):
    # A blank value, as produced by `SECRET_KEY=${SECRET_KEY:-}` in compose, means "unset".
    settings = make(tmp_path, SECRET_KEY="")
    key = settings.resolve_secret_key()
    key_file = tmp_path / "db" / "secret_key"

    assert len(key) >= 43
    assert key_file.read_text() == key
    assert settings.resolve_secret_key() == key
    assert make(tmp_path).resolve_secret_key() == key
    if os.name == "posix":
        assert stat.S_IMODE(key_file.stat().st_mode) == 0o600


def test_empty_secret_key_file_is_regenerated(tmp_path):
    key_file = tmp_path / "db" / "secret_key"
    key_file.parent.mkdir()
    key_file.write_text("  \n")
    key = make(tmp_path).resolve_secret_key()
    assert key
    assert key_file.read_text() == key
