#!/usr/bin/env bash
# SKIP: mutating — push 시작 시점 기준의 지연 측정은 테스트 push 발행이 필요 (설계자 승인 대기)
# A2-5 (재생 지연): push 시작 콘텐츠가 HLS로 시청 가능해지기까지 15초 이내면 OK.
set -u

if [ "${SPEC_TDD_ALLOW_MUTATING:-0}" != "1" ]; then
  echo "SKIPPED: requires starting a test RTMP push (state transition). Set SPEC_TDD_ALLOW_MUTATING=1 to run."
  exit 2
fi

KEY="${STREAM_KEY:-spec-test-a25}"
docker run -d --rm --name spec-a25-push --network sentinel_sentinel-net --entrypoint ffmpeg linuxserver/ffmpeg \
  -f lavfi -i "testsrc=size=640x360:rate=15" -f lavfi -i sine \
  -t 60 -c:v libx264 -profile:v baseline -tune zerolatency -bf 0 -g 30 -c:a aac \
  -f flv "rtmp://streaming:1935/live/${KEY}" >/dev/null
trap 'docker rm -f spec-a25-push >/dev/null 2>&1 || true' EXIT
START=$(date +%s)

DEADLINE=$((START + 15))
while [ "$(date +%s)" -le "$DEADLINE" ]; do
  if timeout 10 docker exec sentinel-cctv-adapter ffprobe -v error -show_entries stream=codec_name \
      "http://streaming:8080/live/${KEY}/index.m3u8" >/dev/null 2>&1; then
    echo "OK: HLS playable within $(( $(date +%s) - START ))s"
    exit 0
  fi
  sleep 1
done
echo "NOK: HLS not playable within 15s"
exit 1
