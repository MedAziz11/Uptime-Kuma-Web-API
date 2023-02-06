from fastapi import FastAPI
from fastapi.responses import RedirectResponse
import os

from routers import monitor, auth, tags, cert, info, uptime, ping, database, settings as settings, user, maintenance
from config import settings as app_settings
from utils.admin import check_admin

from tortoise import Tortoise


app = FastAPI(title=app_settings.PROJECT_NAME)
app.router.redirect_slashes = True

app.include_router(user.router, prefix="/users", tags=["Users"])
app.include_router(settings.router, prefix="/settings", tags=["Settings"])
app.include_router(database.router, prefix="/database", tags=["DataBase"])
app.include_router(monitor.router, prefix="/monitors", tags=["Monitor"])
app.include_router(maintenance.router, prefix="/maintenance", tags=["Maintenance"])
app.include_router(tags.router, prefix="/tags", tags=["Tags"])
app.include_router(cert.router, prefix="/cert_info", tags=["Certification Info"])
app.include_router(info.router, prefix="/info", tags=["Informations"])
app.include_router(ping.router, prefix="/ping", tags=["Ping Average"])
app.include_router(uptime.router, prefix="/uptime", tags=["Uptime"])
app.include_router(auth.router, prefix="/login", tags=["Authentication"])

@app.on_event("startup")
async def startup_event():
    if not os.path.exists("../db"):
        os.makedirs("../db")

    await Tortoise.init(
        db_url="sqlite://../db/test.sqlite3",
        modules={"models": ["models.user"]}
    )

    await Tortoise.generate_schemas()



    await check_admin()


@app.on_event("shutdown")
async def shutdown_event():
    await Tortoise.close_connections()


@app.get("/", include_in_schema=False)
async def root():
    return RedirectResponse(url='/docs')