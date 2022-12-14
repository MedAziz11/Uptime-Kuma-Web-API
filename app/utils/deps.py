from typing import Optional
from fastapi.security import OAuth2PasswordBearer
from fastapi import Depends, HTTPException
from uptime_kuma_api import UptimeKumaApi, UptimeKumaException


import jwt
from pydantic import ValidationError

from utils.security import verify_password
from schemas.jwt import JWTData
from models.user import UserCreate, UserResponse
from config import settings, logger as logging
from utils import security

oauth2_token = OAuth2PasswordBearer(
    tokenUrl= "/login/access-token/"
)

async def get_current_user(token: str = Depends(oauth2_token)):
    try:
        payload = jwt.decode(
            token, 
            settings.SECRET_KEY, algorithms = [security.ALGORITHM]
            )
        token_data = JWTData(**payload)     
        
    except (jwt.exceptions.InvalidSignatureError, ValidationError) as e:
        logging.info(e)
        raise HTTPException(
            status_code=403,
            detail="invalid credentials"
        )
    try :
    
        api = UptimeKumaApi(settings.KUMA_SERVER)
        api.login_by_token(token_data.sub)
        user ={"token": token_data.sub, "api":api}
        return user
    except UptimeKumaException as e:
        logging.fatal(e)
        raise HTTPException(400, {"error": str(e)})


def authenticate(user: UserCreate, password:str)-> Optional[UserResponse]:
    if not user:
        return None

    if not verify_password(password, user.password_hash):
        return None
    return user

