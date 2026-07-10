#!/usr/bin/env bash
# A5-1: GET /healthz → HTTP 200 + {"status":"ok","service":"streaming"} 이면 OK.
# READ-ONLY.
set -u

# NOTE: healthz body has no trailing newline, so `wget -S -qO-` merges the JSON
# body with the first header line (`{...}  HTTP/1.1 200 OK`) → jq parse fails
# (false-NOK on a compliant server). Fetch status code and body separately.
HDR=$(docker exec sentinel-cctv-adapter wget -S -qO /dev/null http://streaming:8080/healthz 2>&1)
BODY=$(docker exec sentinel-cctv-adapter wget -qO- http://streaming:8080/healthz 2>/dev/null)
echo "$HDR"; echo "body: $BODY"
printf '%s\n' "$HDR" | grep -q 'HTTP/1.1 200' || { echo "NOK: not 200"; exit 1; }
printf '%s' "$BODY" | jq -e '.status=="ok" and .service=="streaming"' >/dev/null \
  && { echo "OK"; exit 0; }
echo "NOK: body mismatch: $BODY"
exit 1
