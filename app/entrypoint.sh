#!/bin/sh

export NAME=uptime-kuma-web-api
export DIR=/app
export USER=appuser
export GROUP=appgroup
export WORKERS=1
export VENV=$DIR/.venv/bin/activate
export WORKER_CLASS=uvicorn.workers.UvicornWorker
export BIND=0.0.0.0:8000
export LOG_LEVEL=info

# cd $DIR

exec gunicorn main:app \
  --name $NAME \
  --workers $WORKERS \
  --worker-class $WORKER_CLASS \
  --user=$USER \
  --group=$GROUP \
  --bind=$BIND \
  --log-level=$LOG_LEVEL 