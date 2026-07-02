#!/usr/bin/env bash
# SKIP: mutating — 규격 준수 테스트 push(spec-test) 발행이 필요 (설계자 승인 대기).
#   read-only 관측(기존 라이브 스트림의 m3u8 형식·ts 항목 포함)은 판정 시 별도 기록되나,
#   "push 시작 후 10초 이내" 시간 조건은 통제된 push 없이는 판정 불가.
# streaming §단언 C (라이브 파이프라인): push 시작 10초 이내 m3u8 200 + #EXTM3U + .ts >= 1.
set -u

if [ "${SPEC_TDD_ALLOW_MUTATING:-0}" != "1" ]; then
  echo "SKIPPED: requires test RTMP push. Set SPEC_TDD_ALLOW_MUTATING=1 to run."
  echo "--- read-only observation on existing live stream (informational) ---"
  KEY=$(docker exec sentinel-cctv-adapter wget -qO- http://streaming:8080/api/streams | jq -r '[.[]|select(.active)][0].streamKey // empty')
  if [ -n "$KEY" ]; then
    M3U8=$(docker exec sentinel-cctv-adapter wget -qO- "http://streaming:8080/live/${KEY}/index.m3u8")
    printf '%s\n' "$M3U8" | head -8
    printf '%s\n' "$M3U8" | head -1 | grep -q '^#EXTM3U' && printf '%s\n' "$M3U8" | grep -q '\.ts$' \
      && echo "info: live pipeline currently serving valid m3u8 with ts entries (streamKey=$KEY)"
  fi
  exit 2
fi

KEY="${STREAM_KEY:-spec-test}"
docker run -d --rm --name spec-c-push --network sentinel_sentinel-net --entrypoint ffmpeg linuxserver/ffmpeg \
  -f lavfi -i "testsrc=size=640x360:rate=15" -f lavfi -i sine \
  -t 60 -c:v libx264 -bf 0 -g 30 -c:a aac \
  -f flv "rtmp://streaming:1935/live/${KEY}" >/dev/null
trap 'docker rm -f spec-c-push >/dev/null 2>&1 || true' EXIT

sleep 10
M3U8=$(docker exec sentinel-cctv-adapter wget -qO- "http://streaming:8080/live/${KEY}/index.m3u8")
RC=$?
printf '%s\n' "$M3U8" | head -8
if [ "$RC" -eq 0 ] && printf '%s\n' "$M3U8" | head -1 | grep -q '^#EXTM3U' \
   && printf '%s\n' "$M3U8" | grep -q '\.ts$'; then
  echo "OK"; exit 0
fi
echo "NOK: m3u8 invalid or missing ts entries within 10s"
exit 1
