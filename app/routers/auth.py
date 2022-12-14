from fastapi import APIRouter, Depends, HTTPException
from fastapi.security import OAuth2PasswordRequestForm
from datetime import datetime, timedelta
from uptime_kuma_api import UptimeKumaException, UptimeKumaApi


from schemas.jwt import JWToken
from models.user import UserCreate, Users

from config import settings
from utils.security import create_access_token
from utils.deps import authenticate
from config import logger as logging, settings


router = APIRouter()


@router.post("/access-token/", response_model = JWToken)
async def login_access_token(form_data : OAuth2PasswordRequestForm = Depends()):
    user_obj = await Users.get_or_none(username=form_data.username)
    user = authenticate(user_obj, form_data.password)
    if not user:
        logging.info("Incorrect username or password")
        raise HTTPException(400, {"message": "Incorrect username or password"})

    try :
        user.last_visit = datetime.now()
        await user.save(update_fields=["last_visit"])

        logging.fatal(f"hellooooooooooooo  {settings.KUMA_SERVER}")
        api = UptimeKumaApi(settings.KUMA_SERVER)
        resp = api.login(settings.KUMA_USERNAME, settings.KUMA_PASSWORD)

        logging.info("Logged in to UptimeKuma")

        access_token_expires = timedelta(minutes = settings.ACCESS_TOKEN_EXPIRE)
        response = {"access_token":create_access_token(resp["token"], access_token_expires),"token_type": "bearer"}

    except UptimeKumaException as e :
        logging.info(e)
        raise HTTPException(400, {"message": "Incorrect Kuma credentials"})
    except Exception as e:
        logging.fatal(e)
        raise HTTPException(400, str(e))

    return JWToken(**response)