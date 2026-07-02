#!/usr/bin/env bash
# SKIP: mutating — 테스트 스트림 RTMP push는 프로덕션 streaming 서버에 스트림을 생성함 (설계자 승인 대기)
# A1-1 (정상 push 지속): B-frame 없는 테스트 스트림 60초 push 시 FFmpeg가 조기 종료하지 않으면 OK.
set -u

if [ "${SPEC_TDD_ALLOW_MUTATING:-0}" != "1" ]; then
  echo "SKIPPED: mutating assertion (RTMP push to production streaming). Set SPEC_TDD_ALLOW_MUTATING=1 to run."
  exit 2
fi

KEY="${STREAM_KEY:-spec-test-a1}"
START=$(date +%s)
docker run --rm --network sentinel_sentinel-net --entrypoint ffmpeg linuxserver/ffmpeg \
  -f lavfi -i "testsrc=size=640x360:rate=15" -f lavfi -i sine \
  -t 60 -c:v libx264 -profile:v baseline -tune zerolatency -bf 0 -g 30 -c:a aac \
  -f flv "rtmp://streaming:1935/live/${KEY}"
RC=$?
ELAPSED=$(( $(date +%s) - START ))
echo "ffmpeg rc=$RC elapsed=${ELAPSED}s"
if [ "$RC" -eq 0 ] && [ "$ELAPSED" -ge 55 ]; then
  echo "OK: push sustained ~60s without early termination"
  exit 0
fi
echo "NOK: push terminated early (rc=$RC, elapsed=${ELAPSED}s)"
exit 1
