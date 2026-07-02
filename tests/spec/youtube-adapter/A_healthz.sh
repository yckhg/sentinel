#!/usr/bin/env bash
# youtube-adapter §단언 A (헬스): GET /healthz → HTTP 200 + {"status":"ok","service":"youtube-adapter"}.
# READ-ONLY.
set -u

HDR=$(docker exec sentinel-youtube-adapter sh -c 'wget -S -qO /dev/null http://localhost:8080/healthz 2>&1')
BODY=$(docker exec sentinel-youtube-adapter wget -qO- http://localhost:8080/healthz)
echo "$HDR"; echo "body: $BODY"
printf '%s\n' "$HDR" | grep -q 'HTTP/1.1 200' || { echo "NOK: not 200"; exit 1; }
# JSON 의미 비교 (키 순서 무관 — 실측 body는 {"service":...,"status":...} 순서)
printf '%s' "$BODY" | jq -e '. == {"status":"ok","service":"youtube-adapter"}' >/dev/null \
  && { echo "OK"; exit 0; }
echo "NOK: body mismatch: $BODY"
exit 1
