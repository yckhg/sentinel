#!/usr/bin/env bash
# SKIP: mutating — push 중단(상태 전이 유발)이 필요. 프로덕션 스트림 중단 불가,
#   테스트 push 발행도 승인 대기.
# A4-3 (비활성 전환): push 중단 후 40초 뒤 해당 항목이 목록에 없거나 active:false 면 OK.
set -u

if [ "${SPEC_TDD_ALLOW_MUTATING:-0}" != "1" ]; then
  echo "SKIPPED: requires push stop (state transition). Set SPEC_TDD_ALLOW_MUTATING=1 to run."
  exit 2
fi

KEY="${STREAM_KEY:-spec-test-a1}"
# 40초 테스트 push 후 자연 종료 → 40초 대기 → 판정
docker run --rm --network sentinel_sentinel-net --entrypoint ffmpeg linuxserver/ffmpeg \
  -re -f lavfi -i "testsrc=size=640x360:rate=15" -f lavfi -i sine \
  -t 40 -c:v libx264 -pix_fmt yuv420p -profile:v baseline -tune zerolatency -bf 0 -g 30 -c:a aac \
  -f flv "rtmp://streaming:1935/live/${KEY}" >/dev/null 2>&1
sleep 40
docker exec sentinel-cctv-adapter wget -qO- http://streaming:8080/api/streams | \
  jq -e "[.[] | select(.streamKey==\"$KEY\" and .active==true)] | length == 0" \
  && { echo "OK: no active entry 40s after push stop"; exit 0; }
echo "NOK: entry still active:true 40s after push stop"
exit 1
