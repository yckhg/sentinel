#!/usr/bin/env bash
# streaming §단언 D (active 판정 SSOT):
#   전반부(push 진행 중 → active:true): READ-ONLY로 판정 — 어댑터가 push 중(status running)인
#   기존 스트림이 /api/streams 에 active:true 로 반영되는지 확인.
#   후반부(push 중단 → 판정 창 + 5초 후 active:false/소멸): 상태 전이 유발 필요 → SKIP
#   (SPEC_TDD_ALLOW_MUTATING=1 로 테스트 push 사용 시에만 실행).
set -u

# --- 전반부: push 진행 중 active:true (read-only) ---
PUSHING=$(docker exec sentinel-youtube-adapter wget -qO- http://localhost:8080/api/streams/status | jq -r '[.[]|select(.status=="running")][0].streamKey // empty')
if [ -z "$PUSHING" ]; then echo "NOK: no adapter-side running push to cross-check"; exit 1; fi
echo "adapter reports running push: $PUSHING"

docker exec sentinel-cctv-adapter wget -qO- http://streaming:8080/api/streams | \
  jq -e ".[] | select(.streamKey==\"$PUSHING\") | .active == true" >/dev/null \
  || { echo "NOK: running push not active:true in /api/streams"; exit 1; }
echo "first half OK: streamKey=$PUSHING is active:true while push in progress"

# --- 후반부: push 중단 후 전환 (mutating) ---
if [ "${SPEC_TDD_ALLOW_MUTATING:-0}" != "1" ]; then
  echo "SKIPPED(second half): push stop transition requires mutating test push."
  exit 2
fi
KEY="spec-test"
docker run --rm --network sentinel_sentinel-net --entrypoint ffmpeg linuxserver/ffmpeg \
  -f lavfi -i "testsrc=size=640x360:rate=15" -f lavfi -i sine \
  -t 30 -c:v libx264 -bf 0 -g 30 -c:a aac \
  -f flv "rtmp://streaming:1935/live/${KEY}" >/dev/null 2>&1
sleep 35   # 판정 창 30초 + 여유 5초
docker exec sentinel-cctv-adapter wget -qO- http://streaming:8080/api/streams | \
  jq -e "[.[] | select(.streamKey==\"$KEY\" and .active==true)] | length == 0" >/dev/null \
  && { echo "OK: entry gone or active:false after window+5s"; exit 0; }
echo "NOK: still active:true after push stop + window"
exit 1
