#!/usr/bin/env bash
# cctv-adapter §단언 A (헬스): GET /healthz → 200 + {"status":"ok","service":"cctv-adapter"}.
#   카메라 0대 상태에서도 동일하게 200. (현재 프로덕션 구성이 카메라 0대라 그 조건도 함께 판정)
# READ-ONLY.
set -u

HDR=$(docker exec sentinel-cctv-adapter sh -c 'wget -S -qO /dev/null http://localhost:8080/healthz 2>&1')
BODY=$(docker exec sentinel-cctv-adapter wget -qO- http://localhost:8080/healthz)
N=$(docker exec sentinel-cctv-adapter wget -qO- http://localhost:8080/api/cameras/status | jq 'length')
echo "$HDR"; echo "body: $BODY"; echo "configured cameras: $N"

printf '%s\n' "$HDR" | grep -q 'HTTP/1.1 200' || { echo "NOK: not 200"; exit 1; }
printf '%s' "$BODY" | jq -e '. == {"status":"ok","service":"cctv-adapter"}' >/dev/null \
  && { echo "OK (cameras=$N)"; exit 0; }
echo "NOK: body mismatch: $BODY"
exit 1
