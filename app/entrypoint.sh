#!/usr/bin/env sh

NAME=uptime-kuma-web-api
DIR=/app
USER=appuser
GROUP=appgroup
WORKERS=1
VENV=$DIR/.venv/bin/activate
WORKER_CLASS=uvicorn.workers.UvicornWorker
BIND=0.0.0.0:8000
LOG_LEVEL=info

cd $DIR

exec gunicorn main:app \
  --name $NAME \
  --workers $WORKERS \
  --worker-class $WORKER_CLASS \
  --user=$USER \
  --group=$GROUP \
  --bind=$BIND \
  --log-level=$LOG_LEVEL \
  --log-file=-