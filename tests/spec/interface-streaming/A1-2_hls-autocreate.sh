#!/usr/bin/env bash
# SKIP: mutating — A1-1의 테스트 push에 의존 (프로덕션 streaming에 스트림 생성; 설계자 승인 대기)
# A1-2 (HLS 자동 생성): A1-1 push 시작 후 10초 이내 index.m3u8이 HTTP 200 + '#EXTM3U'로 응답하면 OK.
set -u

if [ "${SPEC_TDD_ALLOW_MUTATING:-0}" != "1" ]; then
  echo "SKIPPED: depends on mutating A1-1 test push. Set SPEC_TDD_ALLOW_MUTATING=1 to run."
  exit 2
fi

KEY="${STREAM_KEY:-spec-test-a1}"
# A1-1 push를 백그라운드로 시작
docker run -d --rm --name spec-a1-push --network sentinel_sentinel-net --entrypoint ffmpeg linuxserver/ffmpeg \
  -f lavfi -i "testsrc=size=640x360:rate=15" -f lavfi -i sine \
  -t 60 -c:v libx264 -profile:v baseline -tune zerolatency -bf 0 -g 30 -c:a aac \
  -f flv "rtmp://streaming:1935/live/${KEY}" >/dev/null
trap 'docker rm -f spec-a1-push >/dev/null 2>&1 || true' EXIT

sleep 10
BODY=$(docker exec sentinel-cctv-adapter wget -qO- "http://streaming:8080/live/${KEY}/index.m3u8" 2>/dev/null)
RC=$?
echo "--- m3u8 (10s after push start) ---"; echo "$BODY" | head -5
if [ "$RC" -eq 0 ] && printf '%s' "$BODY" | head -1 | grep -q '^#EXTM3U'; then
  echo "OK: m3u8 served with #EXTM3U within 10s"
  exit 0
fi
echo "NOK: m3u8 not available/valid within 10s (rc=$RC)"
exit 1
