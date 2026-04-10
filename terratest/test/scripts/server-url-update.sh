#!/bin/bash

set -euo pipefail

: "${SERVER_URL:?SERVER_URL must be set}"
: "${RANCHER_TOKEN:?RANCHER_TOKEN must be set}"

# Configure the server-url
curl --fail --silent --show-error --insecure -X PUT "https://${SERVER_URL}/v3/settings/server-url" \
  -H "Authorization: Bearer ${RANCHER_TOKEN}" \
  -H 'Content-Type: application/json' \
  --data-binary "{\"name\": \"server-url\", \"value\":\"https://${SERVER_URL}\"}"
