#!/bin/bash

set -x

SERVER_URL=${SERVER_URL}
RANCHER_TOKEN=${RANCHER_TOKEN}

# Configure the server-url
curl -s -k -X PUT "https://${SERVER_URL}/v3/settings/server-url" \
  -H "Authorization: Bearer ${RANCHER_TOKEN}" \
  -H 'Content-Type: application/json' \
  --data-binary "{\"name\": \"server-url\", \"value\":\"${SERVER_URL}\"}"
