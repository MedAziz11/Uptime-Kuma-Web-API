#!/bin/bash

TOKEN=$(curl -X 'POST' \
  'http://localhost:8000/login/access-token' \
  -H 'accept: application/json' \
  -H 'Content-Type: application/x-www-form-urlencoded' \
  -d 'grant_type=&username=admin&password=admin&scope=&client_id=&client_secret=' | jq -r ".access_token")
echo -e "\nToken: ${TOKEN}"

echo -e "\nGet all status pages:"
curl -L -H 'Accept: application/json' -H "Authorization: Bearer ${TOKEN}" http://localhost:8000/statuspages

echo -e "\nAdd a status page:"
curl -X 'POST' \
  -H "Authorization: Bearer ${TOKEN}" \
  -H 'Content-Type: application/json' \
  -d '{"title": "New Page", "slug": "hsl-dev", "msg": "Initial message"}' \
  'http://localhost:8000/statuspages'

echo -e "\nGet a specific status page:"
curl -L -H 'Accept: application/json' -H "Authorization: Bearer ${TOKEN}" http://localhost:8000/statuspages/hsl-dev
STATUS_PAGE_ID=$(curl -L -H 'Accept: application/json' -H "Authorization: Bearer ${TOKEN}" http://localhost:8000/statuspages/hsl-dev | jq -r ".id")
echo -e "\nStatus Page ID: ${STATUS_PAGE_ID}"

echo -e "\nSave a status page:"
curl -X 'POST' \
  -H "Authorization: Bearer ${TOKEN}" \
  -H 'Content-Type: application/json' \
  -d '{
    "id": '${STATUS_PAGE_ID}',
    "slug": "hsl-dev",
    "title": "Curl Updated Title",
    "description": "Some Description",
    "theme": "dark",
    "published": true,
    "showTags": true,
    "domainNameList": ["https://call-dev.iccsafe.org", "https://call-dev-rtm.iccsafe.org"],
    "googleAnalyticsId": "UA-123456789-1",
    "customCSS": "body { background-color: red; }",
    "footerText": "Custom Footer Text",
    "showPoweredBy": false,
    "icon": "/icon.svg",
    "publicGroupList": [
        {
            "id": 1,
            "name": "HSL DEV Monitoring Group",
            "monitorList": [
                {
                    "id": 1,
                    "name": "Monitor 1",
                    "url": "https://call-dev.iccsafe.org/healthchecks/ping",
                    "type": "http"
                },
                {
                    "id": 2,
                    "name": "Monitor 1",
                    "url": "https://call-dev-rtm.iccsafe.org/healthchecks/ping",
                    "type": "http"
                }
            ]
        }
    ]
  }' \
  'http://localhost:8000/statuspages/hsl-dev'

# echo -e "\nDelete a status page:"
# curl -X 'DELETE' \
#   -H "Authorization: Bearer ${TOKEN}" \
#   'http://localhost:8000/statuspages/hsl-dev'

echo -e "\nPost an incident:"
INCIDENT_ID=$(curl -X 'POST' \
  -H "Authorization: Bearer ${TOKEN}" \
  -H 'Content-Type: application/json' \
  -d '{
    "title": "Incident Title",
    "content": "Incident Content",
    "style": "danger"
  }' \
  'http://localhost:8000/statuspages/hsl-dev/incident' | jq -r ".id")
echo -e "\nIncident ID: ${INCIDENT_ID}"

sleep 5

echo -e "\nUnpin an incident:"
curl -X 'DELETE' \
  -H "Authorization: Bearer ${TOKEN}" \
  'http://localhost:8000/statuspages/hsl-dev/incident/unpin'
