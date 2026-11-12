from datetime import UTC, datetime, timedelta

import bcrypt
import jwt

ALGORITHM = "HS256"

# bcrypt only reads the first 72 bytes of a password and bcrypt>=5 rejects longer ones.
MAX_PASSWORD_BYTES = 72

# Verified against when the user does not exist, so that response time does not
# reveal whether a username is valid.
_DUMMY_HASH = bcrypt.hashpw(b"dummy-password", bcrypt.gensalt()).decode()


def hash_password(password: str) -> str:
    return bcrypt.hashpw(password.encode(), bcrypt.gensalt()).decode()


def verify_password(password: str, hashed_password: str | None) -> bool:
    try:
        return bcrypt.checkpw(
            password.encode(), (hashed_password or _DUMMY_HASH).encode()
        ) and bool(hashed_password)
    except ValueError:  # malformed hash or password longer than 72 bytes
        return False


def create_access_token(subject: str, secret_key: str, expires_in: timedelta) -> str:
    now = datetime.now(UTC)
    payload = {"sub": subject, "iat": now, "exp": now + expires_in}
    return jwt.encode(payload, secret_key, algorithm=ALGORITHM)


def decode_access_token(token: str, secret_key: str) -> str:
    """Return the token subject. Raises `jwt.InvalidTokenError` when the token is not valid."""
    payload = jwt.decode(
        token, secret_key, algorithms=[ALGORITHM], options={"require": ["exp", "sub"]}
    )
    subject = payload["sub"]
    if not isinstance(subject, str) or not subject:
        raise jwt.InvalidTokenError("invalid subject")
    return subject
