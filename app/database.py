import logging

from fastapi import FastAPI
from tortoise.contrib.fastapi import RegisterTortoise

from app.config import ADMIN_USERNAME, Settings
from app.models import Users
from app.security import hash_password, verify_password

logger = logging.getLogger(__name__)


def register_db(app: FastAPI, settings: Settings) -> RegisterTortoise:
    """Async context manager that opens the SQLite database for the app's lifetime."""
    settings.DB_PATH.parent.mkdir(parents=True, exist_ok=True)
    return RegisterTortoise(
        app,
        db_url=f"sqlite://{settings.DB_PATH.as_posix()}",
        modules={"models": ["app.models"]},
        generate_schemas=True,
    )


async def ensure_admin(settings: Settings) -> None:
    """Create the admin user, or realign its password with ADMIN_PASSWORD."""
    password = settings.ADMIN_PASSWORD.get_secret_value()
    admin = await Users.get_or_none(username=ADMIN_USERNAME)
    if admin is None:
        await Users.create(username=ADMIN_USERNAME, password_hash=hash_password(password))
        logger.info("Created the '%s' user", ADMIN_USERNAME)
    elif not verify_password(password, admin.password_hash):
        admin.password_hash = hash_password(password)
        await admin.save(update_fields=["password_hash"])
        logger.info("Updated the '%s' password from ADMIN_PASSWORD", ADMIN_USERNAME)
