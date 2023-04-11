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
  -d '{"title": "New Page", "slug": "new-page", "msg": "Initial message"}' \
  'http://localhost:8000/statuspages'

echo -e "\nGet a specific status page:"
curl -L -H 'Accept: application/json' -H "Authorization: Bearer ${TOKEN}" http://localhost:8000/statuspages/new-page
STATUS_PAGE_ID=$(curl -L -H 'Accept: application/json' -H "Authorization: Bearer ${TOKEN}" http://localhost:8000/statuspages/new-page | jq -r ".id")
echo -e "\nStatus Page ID: ${STATUS_PAGE_ID}"

echo -e "\nSave a status page:"
curl -X 'POST' \
  -H "Authorization: Bearer ${TOKEN}" \
  -H 'Content-Type: application/json' \
  -d '{
    "id": '${STATUS_PAGE_ID}',
    "slug": "new-page",
    "title": "Curl Updated Title",
    "description": "Some Description",
    "theme": "dark",
    "published": true,
    "showTags": true,
    "domainNameList": ["https://example.com", "https://test.com"],
    "googleAnalyticsId": "UA-123456789-1",
    "customCSS": "body { background-color: red; }",
    "footerText": "Custom Footer Text",
    "showPoweredBy": false,
    "icon": "/icon.svg",
    "publicGroupList": [
        {
            "id": 1,
            "name": "Test Monitoring Group",
            "monitorList": [
                {
                    "id": 1,
                    "name": "Monitor 1",
                    "url": "https://example.com",
                    "type": "http"
                },
                {
                    "id": 2,
                    "name": "Monitor 1",
                    "url": "https://test.com",
                    "type": "http"
                }
            ]
        }
    ]
  }' \
  'http://localhost:8000/statuspages/new-page'

echo -e "\nDelete a status page:"
curl -X 'DELETE' \
  -H "Authorization: Bearer ${TOKEN}" \
  'http://localhost:8000/statuspages/new-page'

echo -e "\nPost an incident:"
INCIDENT_ID=$(curl -X 'POST' \
  -H "Authorization: Bearer ${TOKEN}" \
  -H 'Content-Type: application/json' \
  -d '{
    "title": "Incident Title",
    "content": "Incident Content",
    "style": "danger"
  }' \
  'http://localhost:8000/statuspages/new-page/incident' | jq -r ".id")
echo -e "\nIncident ID: ${INCIDENT_ID}"

sleep 5

echo -e "\nUnpin an incident:"
curl -X 'DELETE' \
  -H "Authorization: Bearer ${TOKEN}" \
  'http://localhost:8000/statuspages/new-page/incident/unpin'
