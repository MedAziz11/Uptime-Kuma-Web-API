from datetime import UTC, datetime, timedelta

import jwt
import pytest

from app.security import (
    ALGORITHM,
    MAX_PASSWORD_BYTES,
    create_access_token,
    decode_access_token,
    hash_password,
    verify_password,
)

KEY = "unit-test-signing-key-unit-test-signing-key"


def test_password_round_trip():
    hashed = hash_password("correct horse")
    assert hashed.startswith("$2b$")
    assert hashed != hash_password("correct horse")  # salted
    assert verify_password("correct horse", hashed)
    assert not verify_password("wrong horse", hashed)


def test_unicode_password():
    hashed = hash_password("pässwörd-密码")
    assert verify_password("pässwörd-密码", hashed)
    assert not verify_password("password-密码", hashed)


@pytest.mark.parametrize("hashed", [None, "", "not-a-bcrypt-hash", "$2b$12$tooshort"])
def test_missing_or_malformed_hash_never_verifies(hashed):
    assert verify_password("anything", hashed) is False


def test_overlong_password_never_verifies():
    hashed = hash_password("x" * MAX_PASSWORD_BYTES)
    assert verify_password("x" * MAX_PASSWORD_BYTES, hashed)
    assert verify_password("x" * (MAX_PASSWORD_BYTES + 1), hashed) is False


def test_token_round_trip():
    token = create_access_token("alice", KEY, timedelta(minutes=5))
    assert decode_access_token(token, KEY) == "alice"

    payload = jwt.decode(token, KEY, algorithms=[ALGORITHM])
    assert payload["exp"] - payload["iat"] == 300


def forge(payload: dict, key: str = KEY, algorithm: str = ALGORITHM) -> str:
    return jwt.encode(payload, key, algorithm=algorithm)


def in_one_hour() -> datetime:
    return datetime.now(UTC) + timedelta(hours=1)


@pytest.mark.parametrize(
    "token",
    [
        "",
        "garbage",
        create_access_token("alice", KEY, timedelta(seconds=-1)),  # expired
        create_access_token("alice", "another-key-another-key-another-key", timedelta(hours=1)),
        forge({"sub": "alice"}),  # no expiry
        forge({"exp": in_one_hour()}),  # no subject
        forge({"sub": "", "exp": in_one_hour()}),  # empty subject
        forge({"sub": "alice", "exp": in_one_hour()}, key="", algorithm="none"),  # unsigned
        forge({"sub": "alice", "exp": in_one_hour()}, key=KEY * 2, algorithm="HS512"),
    ],
)
def test_invalid_tokens_are_rejected(token):
    with pytest.raises(jwt.InvalidTokenError):
        decode_access_token(token, KEY)
