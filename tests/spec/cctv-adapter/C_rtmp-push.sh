#!/usr/bin/env bash
# SKIP: 관측 불가 + mutating — 현재 프로덕션 cctv-adapter는 카메라 0대 구성
#   (cameras.json == []). 판정하려면 유효 RTSP 소스를 부트 설정에 추가하고 재기동해야 함
#   (설정 변경 + 컨테이너 재시작 = mutating, 설계자 승인 대기).
# cctv-adapter §단언 C (RTMP push 성립): 유효 RTSP 소스(camX) 기동 후 30초 이내
#   /live/camX/index.m3u8 재생 가능(또는 /api/streams 에서 active:true)이면 OK.
set -u

if [ "${SPEC_TDD_ALLOW_MUTATING:-0}" != "1" ]; then
  echo "SKIPPED: no RTSP camera configured in production (cameras.json is []); requires config change + restart."
  exit 2
fi

KEY="${STREAM_KEY:?set STREAM_KEY to the configured cameraId}"
DEADLINE=$(( $(date +%s) + 30 ))
while [ "$(date +%s)" -le "$DEADLINE" ]; do
  docker exec sentinel-cctv-adapter wget -qO- http://streaming:8080/api/streams | \
    jq -e ".[] | select(.streamKey==\"$KEY\") | .active == true" >/dev/null 2>&1 \
    && { echo "OK: $KEY active within 30s"; exit 0; }
  sleep 2
done
echo "NOK: $KEY not active within 30s"
exit 1
