#!/bin/bash

BASE_URL="${BASE_URL:-http://127.0.0.1:8000}"

TOKEN=$(curl -s -X 'POST' \
  "${BASE_URL}/login/access-token" \
  -H 'accept: application/json' \
  -H 'Content-Type: application/x-www-form-urlencoded' \
  --data-urlencode 'username=admin' --data-urlencode "password=${ADMIN_PASSWORD:?set ADMIN_PASSWORD}" | jq -r ".access_token")

echo "Token: ${TOKEN}"

echo "Get all monitor pages:"
curl -s -L -H 'Accept: application/json' -H "Authorization: Bearer ${TOKEN}" "${BASE_URL}/monitors"
