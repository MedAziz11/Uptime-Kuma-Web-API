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

api = UptimeKumaApi(settings.KUMA_SERVER)


async def get_current_user(token: str = Depends(oauth2_token)):
    try:
        payload = jwt.decode(
            token, 
            settings.SECRET_KEY, algorithms = [security.ALGORITHM]
            )
        token_data = JWTData(**payload)     
        
        user ={"token": token_data.sub, "api":api}
        return user
        
    except (jwt.exceptions.InvalidSignatureError, ValidationError) as e:
        logging.info(e)
        api.disconnect()
        raise HTTPException(
            status_code=403,
            detail="invalid credentials"
        )
        
    except jwt.exceptions.ExpiredSignatureError as e:
        logging.info(e)
        api.disconnect()
        raise HTTPException(
            status_code=403,
            detail="Token expired !!"
        )
    
        
    except UptimeKumaException as e:
        logging.fatal(e)
        raise HTTPException(400, {"error": str(e)})


def authenticate(user: UserCreate, password:str)-> Optional[UserResponse]:
    if not user:
        return None

    if not verify_password(password, user.password_hash):
        return None
    
    
        
    try :
        logging.fatal(f"hello from {settings.KUMA_SERVER}")
        api.login(settings.KUMA_USERNAME, settings.KUMA_PASSWORD)
        logging.info("Logged in to UptimeKuma")
        
    except UptimeKumaException as e :
        logging.info(e)
        raise HTTPException(400, {"message": "Incorrect Kuma credentials"})
    except Exception as e:
        logging.fatal("maybe an error with connection try disconnect the socket",e)
        raise HTTPException(400, str(e))

    return user

