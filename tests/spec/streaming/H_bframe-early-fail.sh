#!/usr/bin/env bash
# SKIP: mutating — B-frame 포함 테스트 push 발행이 필요 (설계자 승인 대기).
#   interface-streaming A1-3 과 동일 쟁점 (규격 위반 입력의 조기 실패 회귀 확인).
# streaming §단언 H: -bf 2 push 가 수 초 내 리셋되거나 30초 시점 m3u8 미갱신이면 OK.
set -u

if [ "${SPEC_TDD_ALLOW_MUTATING:-0}" != "1" ]; then
  echo "SKIPPED: mutating B-frame test push. Set SPEC_TDD_ALLOW_MUTATING=1 to run."
  exit 2
fi

KEY="spec-test-bf"
START=$(date +%s)
timeout 40 docker run --rm --network sentinel_sentinel-net --entrypoint ffmpeg linuxserver/ffmpeg \
  -re -f lavfi -i "testsrc=size=640x360:rate=15" -f lavfi -i sine \
  -t 35 -c:v libx264 -pix_fmt yuv420p -profile:v main -bf 2 -g 30 -c:a aac \
  -f flv "rtmp://streaming:1935/live/${KEY}"
RC=$?
ELAPSED=$(( $(date +%s) - START ))
echo "ffmpeg rc=$RC elapsed=${ELAPSED}s"
if [ "$RC" -ne 0 ] && [ "$ELAPSED" -lt 30 ]; then
  echo "OK: connection reset within seconds (violation not silently accepted)"
  exit 0
fi
# 지속되었다면 m3u8 갱신 여부 확인
M=$(docker exec sentinel-streaming stat -c %Y "/tmp/hls/${KEY}/index.m3u8" 2>/dev/null || echo 0)
NOW=$(date +%s)
if [ "$M" -eq 0 ] || [ $((NOW - M)) -gt 10 ]; then
  echo "OK: m3u8 absent/stale at 30s (violation not silently accepted)"
  exit 0
fi
echo "NOK: B-frame push silently accepted with healthy m3u8"
exit 1
