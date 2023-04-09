#!/bin/bash

TOKEN=$(curl -X 'POST' \
  'http://127.0.0.1:8000/login/access-token' \
  -H 'accept: application/json' \
  -H 'Content-Type: application/x-www-form-urlencoded' \
  -d 'grant_type=&username=admin&password=admin&scope=&client_id=&client_secret=' | jq -r ".access_token")
echo -e "\nToken: ${TOKEN}"

echo -e "\nGet all status pages:"
curl -L -H 'Accept: application/json' -H "Authorization: Bearer ${TOKEN}" http://127.0.0.1:8000/statuspages
echo -e "\nGet a specific status page:"
curl -L -H 'Accept: application/json' -H "Authorization: Bearer ${TOKEN}" http://127.0.0.1:8000/statuspages/hsl-dev

echo -e "\nAdd a status page:"
curl -X 'POST' \
  -H "Authorization: Bearer ${TOKEN}" \
  'http://127.0.0.1:8000/statuspages?slug=slugToDelete&title=slugToDelete'

echo -e "\nSave a status page:"
curl -X 'PUT' \
  -H "Authorization: Bearer ${TOKEN}" \
  -H 'Content-Type: application/json' \
  --data-raw '{
    "id": 2,
    "title": "Updated Title",
    "description": "Updated Description",
    "theme": "dark",
    "published": true,
    "showTags": true,
    "domainNameList": ["example1.com", "example2.com"],
    "googleAnalyticsId": "UA-123456789-1",
    "customCSS": "body { background-color: red; }",
    "footerText": "Custom Footer Text",
    "showPoweredBy": false,
    "icon": "/icon.svg",
    "publicGroupList": [
        {
            "id": 1,
            "name": "Group 1",
            "monitorList": [
                {
                    "id": 1,
                    "name": "Monitor 1",
                    "url": "https://example1.com",
                    "type": "http"
                }
            ]
        }
    ]
  }' \
  'http://127.0.0.1:8000/statuspages/slugToDelete'

echo -e "\nDelete a status page:"
curl -X 'DELETE' \
  -H "Authorization: Bearer ${TOKEN}" \
  'http://127.0.0.1:8000/statuspages/slugToDelete'

