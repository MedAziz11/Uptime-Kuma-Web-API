#!/bin/bash

BASE_URL="http://127.0.0.1:8000"

TOKEN=$(curl -X 'POST' \
  "${BASE_URL}/login/access-token" \
  -H 'accept: application/json' \
  -H 'Content-Type: application/x-www-form-urlencoded' \
  -d 'grant_type=&username=admin&password=admin&scope=&client_id=&client_secret=' | jq -r ".access_token")

echo "Token: ${TOKEN}"

echo "Get all monitor pages:"
curl -L -H 'Accept: application/json' -H "Authorization: Bearer ${TOKEN}" "${BASE_URL}/monitors"
