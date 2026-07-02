#!/usr/bin/env bash
# youtube-adapter §단언 E (RTMP 출력 규격 준수): 송출 결과물을 ffprobe로 검사 —
#   video codec h264 + B-frame 없음(has_b_frames=0), audio codec aac,
#   push 시작 후 60초를 훨씬 넘겨 연결 유지되면 OK.
# READ-ONLY: 기존 라이브 HLS 산출물 ffprobe + 어댑터 상태의 startedAt 로 판정.
set -u

KEY="${STREAM_KEY:-$(docker exec sentinel-youtube-adapter wget -qO- http://localhost:8080/api/streams/status | jq -r '[.[]|select(.status=="running")][0].streamKey // empty')}"
if [ -z "$KEY" ]; then echo "NOK: no running stream"; exit 1; fi
echo "observing streamKey=$KEY"

FAIL=0
V=$(timeout 60 docker exec sentinel-youtube-adapter ffprobe -v error -select_streams v:0 \
  -show_entries stream=codec_name,has_b_frames -of csv=p=0 "http://streaming:8080/live/${KEY}/index.m3u8" 2>&1 | tail -1)
A=$(timeout 60 docker exec sentinel-youtube-adapter ffprobe -v error -select_streams a:0 \
  -show_entries stream=codec_name -of csv=p=0 "http://streaming:8080/live/${KEY}/index.m3u8" 2>&1 | tail -1)
echo "video: $V (codec,has_b_frames)"
echo "audio: $A"
[ "$V" = "h264,0" ] || { echo "FAIL: video not h264/B-frame-free"; FAIL=1; }
[ "$A" = "aac" ] || { echo "FAIL: audio not aac"; FAIL=1; }

# 실프레임 표본에서 B-frame 부재 재확인
BF=$(timeout 90 docker exec sentinel-youtube-adapter ffprobe -v error -select_streams v:0 \
  -show_entries frame=pict_type -of csv=p=0 -read_intervals "%+6" \
  "http://streaming:8080/live/${KEY}/index.m3u8" 2>/dev/null | grep -c '^B' || true)
echo "B-frames in 6s sample: $BF"
[ "${BF:-0}" -eq 0 ] || { echo "FAIL: B-frames present in sample"; FAIL=1; }

# 연결 유지 >> 60초
STARTED=$(docker exec sentinel-youtube-adapter wget -qO- http://localhost:8080/api/streams/status | jq -r ".[]|select(.streamKey==\"$KEY\").startedAt")
AGE=$(( $(date +%s) - $(date -d "$STARTED" +%s) ))
echo "push session age: ${AGE}s (startedAt=$STARTED)"
[ "$AGE" -ge 60 ] || { echo "FAIL: session younger than 60s"; FAIL=1; }

[ "$FAIL" -eq 0 ] && { echo "OK"; exit 0; }
echo "NOK"; exit 1
