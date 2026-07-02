#!/usr/bin/env bash
# SKIP: mutating — spec-test-a1 테스트 push 발행이 필요 (설계자 승인 대기).
#   참고: 동일 판정 로직(active/hlsUrl/cameraId==streamKey)은 기존 라이브 스트림에 대해
#   streaming spec 단언 D·E 스크립트가 read-only로 검증함.
# A4-2 (활성 반영): push 시작 10초 후 /api/streams 에 해당 항목이 active:true 로 있으면 OK.
set -u

if [ "${SPEC_TDD_ALLOW_MUTATING:-0}" != "1" ]; then
  echo "SKIPPED: requires test RTMP push. Set SPEC_TDD_ALLOW_MUTATING=1 to run."
  exit 2
fi

KEY="${STREAM_KEY:-spec-test-a1}"
docker run -d --rm --name spec-a42-push --network sentinel_sentinel-net --entrypoint ffmpeg linuxserver/ffmpeg \
  -f lavfi -i "testsrc=size=640x360:rate=15" -f lavfi -i sine \
  -t 60 -c:v libx264 -profile:v baseline -tune zerolatency -bf 0 -g 30 -c:a aac \
  -f flv "rtmp://streaming:1935/live/${KEY}" >/dev/null
trap 'docker rm -f spec-a42-push >/dev/null 2>&1 || true' EXIT

sleep 10
docker exec sentinel-cctv-adapter wget -qO- http://streaming:8080/api/streams | \
  jq -e ".[] | select(.streamKey==\"$KEY\") | .active == true and .hlsUrl == \"/live/$KEY/index.m3u8\" and .cameraId == .streamKey" \
  && { echo "OK"; exit 0; }
echo "NOK: stream not reflected as active within 10s"
exit 1
