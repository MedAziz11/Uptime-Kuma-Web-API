#!/bin/sh

APP_MODULE=main:app
export HOST=${HOST:-0.0.0.0}
export PORT=${PORT:-8000}  

exec uvicorn ${APP_MODULE} --host=${HOST} --port=${PORT}