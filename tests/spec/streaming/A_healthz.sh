#!/usr/bin/env bash
# streaming §단언 A (헬스): GET /healthz 가 HTTP 200 이고 body JSON의 status == "ok".
# READ-ONLY.
set -u

OUT=$(docker exec sentinel-cctv-adapter wget -S -qO- http://streaming:8080/healthz 2>&1)
echo "$OUT"
printf '%s\n' "$OUT" | grep -q 'HTTP/1.1 200' || { echo "NOK: not 200"; exit 1; }
printf '%s\n' "$OUT" | grep -v '^  ' | jq -e '.status=="ok"' >/dev/null \
  && { echo "OK"; exit 0; }
echo "NOK: status != ok"
exit 1
