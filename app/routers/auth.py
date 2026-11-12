import logging
from datetime import timedelta
from typing import Annotated

from fastapi import APIRouter, Depends, HTTPException, Request, status
from fastapi.security import OAuth2PasswordRequestForm

from app.deps import SettingsDep
from app.models import Users
from app.schemas.auth import Token
from app.security import create_access_token, verify_password

logger = logging.getLogger(__name__)

router = APIRouter()


@router.post("/access-token", response_model=Token, summary="Get an access token")
async def login_access_token(
    request: Request,
    settings: SettingsDep,
    form_data: Annotated[OAuth2PasswordRequestForm, Depends()],
) -> Token:
    user = await Users.get_or_none(username=form_data.username)
    if not verify_password(form_data.password, user.password_hash if user else None):
        logger.info("Failed login for user %r", form_data.username)
        raise HTTPException(
            status_code=status.HTTP_401_UNAUTHORIZED,
            detail="Incorrect username or password",
            headers={"WWW-Authenticate": "Bearer"},
        )

    await user.save(update_fields=["last_visit"])  # refreshes the auto_now field
    token = create_access_token(
        user.username,
        request.app.state.secret_key,
        timedelta(minutes=settings.ACCESS_TOKEN_EXPIRATION),
    )
    return Token(access_token=token)
