#!/usr/bin/env bash
# SKIP: mutating — web-backend 접근 불가 상태를 만들려면 web-backend 중지가 필요하고,
#   POST reload 자체도 상태 변경 API (설계자 승인 대기).
# cctv-adapter §단언 G (reload 실패 경로): web-backend 다운 시 POST reload → 502 + JSON error,
#   기존 카메라 status/connectedAt 불변.
set -u

if [ "${SPEC_TDD_ALLOW_MUTATING:-0}" != "1" ]; then
  echo "SKIPPED: requires stopping web-backend + POST reload."
  exit 2
fi

BEFORE=$(docker exec sentinel-cctv-adapter wget -qO- http://localhost:8080/api/cameras/status)
docker stop sentinel-web-backend >/dev/null
trap 'docker start sentinel-web-backend >/dev/null' EXIT
# NOTE: busybox wget discards the response body on an HTTP error status (502),
# so it cannot see the JSON error body → false-NOK on a compliant adapter.
# Use a raw nc request instead to capture the full response (status + body).
OUT=$(docker exec sentinel-cctv-adapter sh -c 'printf "POST /api/cameras/reload HTTP/1.1\r\nHost: x\r\nContent-Length: 0\r\nConnection: close\r\n\r\n" | nc -w5 127.0.0.1 8080' 2>&1 | tr -d '\r')
echo "$OUT"
AFTER=$(docker exec sentinel-cctv-adapter wget -qO- http://localhost:8080/api/cameras/status)
printf '%s\n' "$OUT" | head -1 | grep -q ' 502' || { echo "NOK: not 502"; exit 1; }
printf '%s\n' "$OUT" | sed '1,/^$/d' | jq -e '.error' >/dev/null || { echo "NOK: no JSON error body"; exit 1; }
[ "$BEFORE" = "$AFTER" ] && { echo "OK: status unchanged"; exit 0; }
echo "NOK: camera status changed on failed reload"
exit 1
