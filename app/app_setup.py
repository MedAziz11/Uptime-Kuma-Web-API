# app_setup.py
import os
from fastapi import FastAPI
from tortoise import Tortoise

from config import settings as app_settings
from utils.admin import check_admin

async def initialize_app(app: FastAPI):
    await setup_database()

    @app.on_event("startup")
    async def startup_event():
        await check_admin()

    @app.on_event("shutdown")
    async def shutdown_event():
        await Tortoise.close_connections()

async def setup_database():
    if not os.path.exists("../db"):
        os.makedirs("../db")

    await Tortoise.init(
        db_url="sqlite://../db/test.sqlite3",
        modules={"models": ["models.user"]}
    )

    await Tortoise.generate_schemas()
