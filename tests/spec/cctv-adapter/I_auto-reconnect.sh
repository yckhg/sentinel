#!/usr/bin/env bash
# SKIP: mutating — RTSP 소스 강제 중단/재개(카메라 또는 네트워크 조작)가 필요하고,
#   전제 상태(단언 C: RTSP 카메라 push 중)도 현재 부재 (설계자 승인 대기).
# cctv-adapter §단언 I (자동 재연결): 소스 중단→재개 후 사람 개입 없이 120초 이내
#   HLS 재생 가능(active:true) 복귀하면 OK.
set -u

if [ "${SPEC_TDD_ALLOW_MUTATING:-0}" != "1" ]; then
  echo "SKIPPED: requires interrupting a live RTSP source; no RTSP camera configured."
  exit 2
fi

KEY="${STREAM_KEY:?set STREAM_KEY}"
echo "Precondition: operator interrupts and restores the RTSP source now (or via test camera container)."
read -rp "Press enter AFTER source is restored..." _
DEADLINE=$(( $(date +%s) + 120 ))
while [ "$(date +%s)" -le "$DEADLINE" ]; do
  docker exec sentinel-cctv-adapter wget -qO- http://streaming:8080/api/streams | \
    jq -e ".[] | select(.streamKey==\"$KEY\") | .active == true" >/dev/null 2>&1 \
    && { echo "OK: recovered within 120s"; exit 0; }
  sleep 5
done
echo "NOK: not recovered within 120s"
exit 1
