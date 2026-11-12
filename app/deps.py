from typing import Annotated

import jwt
from fastapi import Depends, HTTPException, Request, status
from fastapi.security import OAuth2PasswordBearer

from app.config import ADMIN_USERNAME, Settings
from app.kuma import KumaClient
from app.models import Users
from app.security import decode_access_token

oauth2_scheme = OAuth2PasswordBearer(tokenUrl="/login/access-token")


def get_settings(request: Request) -> Settings:
    return request.app.state.settings


def get_kuma(request: Request) -> KumaClient:
    return request.app.state.kuma


async def get_current_user(
    request: Request, token: Annotated[str, Depends(oauth2_scheme)]
) -> Users:
    unauthorized = HTTPException(
        status_code=status.HTTP_401_UNAUTHORIZED,
        detail="Invalid or expired token",
        headers={"WWW-Authenticate": "Bearer"},
    )
    try:
        username = decode_access_token(token, request.app.state.secret_key)
    except jwt.InvalidTokenError:
        raise unauthorized from None

    user = await Users.get_or_none(username=username)
    if user is None:
        raise unauthorized
    return user


async def get_admin_user(user: Annotated[Users, Depends(get_current_user)]) -> Users:
    if user.username != ADMIN_USERNAME:
        raise HTTPException(status.HTTP_403_FORBIDDEN, "Admin privileges required")
    return user


SettingsDep = Annotated[Settings, Depends(get_settings)]
Kuma = Annotated[KumaClient, Depends(get_kuma)]
CurrentUser = Annotated[Users, Depends(get_current_user)]
AdminUser = Annotated[Users, Depends(get_admin_user)]
