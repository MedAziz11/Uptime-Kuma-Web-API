#!/bin/bash

BASE_URL="http://127.0.0.1:8000"
TOKEN=$(curl -X 'POST' \
  "${BASE_URL}/login/access-token" \
  -H 'accept: application/json' \
  -H 'Content-Type: application/x-www-form-urlencoded' \
  -d 'grant_type=&username=admin&password=admin&scope=&client_id=&client_secret=' | jq -r ".access_token")

echo "Token: ${TOKEN} \n"

AUTH_HEADER="Authorization: Bearer ${TOKEN}"

echo "Create a maintenance: \n"
MAINTENANCE_ID=$(curl -X 'POST' \
  ${BASE_URL}/maintenance \
  -H 'accept: application/json' \
  -H "Authorization: Bearer ${TOKEN}" \
  -H 'Content-Type: application/json' \
  -d '{
  "title": "string",
  "strategy": "manual",
  "active": true,
  "description": "",
  "dateRange": [
    "2023-04-11 00:00:00"
  ],
  "intervalDay": 1,
  "weekdays": [],
  "daysOfMonth": [],
  "timeRange": [
    {
      "hours": 2,
      "minutes": 0
    },
    {
      "hours": 3,
      "minutes": 0
    }
  ]
}' | jq -r ".maintenanceID")

echo "Maintenance ID: ${MAINTENANCE_ID}"

echo "Get all maintenances: \n"
curl -X 'GET' \
  ${BASE_URL}/maintenance \
  -H 'accept: application/json' \
  -H "Authorization: Bearer ${TOKEN}"

echo "Get maintenance by ID: \n"
curl -X 'GET' \
  "http://localhost:8000/maintenance/${MAINTENANCE_ID}" \
  -H 'accept: application/json' \
  -H "Authorization: Bearer ${TOKEN}"

echo "Update maintenance: \n"
curl -X 'PATCH' \
  "http://localhost:8000/maintenance/${MAINTENANCE_ID}" \
  -H 'accept: application/json' \
  -H "Authorization: Bearer ${TOKEN}" \
  -H 'Content-Type: application/json' \
  -d '{
  "title": "string",
  "strategy": "manual",
  "active": true,
  "description": "",
  "dateRange": [
    "2023-04-11 00:00:00"
  ],
  "intervalDay": 1,
  "weekdays": [],
  "daysOfMonth": [],
  "timeRange": [
    {
      "hours": 2,
      "minutes": 0
    },
    {
      "hours": 3,
      "minutes": 0
    }
  ]
}'

echo "Pause maintenance: \n"
curl -X 'POST' \
  "http://localhost:8000/maintenance/${MAINTENANCE_ID}/pause" \
  -H 'accept: application/json' \
  -H "Authorization: Bearer ${TOKEN}" \
  -d ''

echo "Resume maintenance: \n"
curl -X 'POST' \
  "http://localhost:8000/maintenance/${MAINTENANCE_ID}/resume" \
  -H 'accept: application/json' \
  -H "Authorization: Bearer ${TOKEN}" \
  -d ''

echo "Add monitors to a maintenance: \n"
curl -X 'POST' \
  "http://localhost:8000/maintenance/${MAINTENANCE_ID}/monitors" \
  -H 'accept: application/json' \
  -H "Authorization: Bearer ${TOKEN}" \
  -H 'Content-Type: application/json' \
  -d '[
  {
    "id": 1,
    "name": "string"
  }
]'

echo "Get monitors to a maintenance: \n"
curl -X 'GET' \
  "http://localhost:8000/maintenance/${MAINTENANCE_ID}/monitors" \
  -H 'accept: application/json' \
  -H "Authorization: Bearer ${TOKEN}"

echo "Delete maintenance: \n"
curl -X 'DELETE' \
  -H 'Accept: application/json' \
  -H "${AUTH_HEADER}" \
  "${BASE_URL}/maintenances/${MAINTENANCE_ID}"
