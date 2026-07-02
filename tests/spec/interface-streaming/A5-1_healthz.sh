#!/usr/bin/env bash
# A5-1: GET /healthz → HTTP 200 + {"status":"ok","service":"streaming"} 이면 OK.
# READ-ONLY.
set -u

OUT=$(docker exec sentinel-cctv-adapter wget -S -qO- http://streaming:8080/healthz 2>&1)
echo "$OUT"
printf '%s\n' "$OUT" | grep -q 'HTTP/1.1 200' || { echo "NOK: not 200"; exit 1; }
BODY=$(printf '%s\n' "$OUT" | grep -v '^  ')
printf '%s' "$BODY" | jq -e '.status=="ok" and .service=="streaming"' >/dev/null \
  && { echo "OK"; exit 0; }
echo "NOK: body mismatch: $BODY"
exit 1
