#!/usr/bin/env bash
# A2-3 (무변환): HLS 출력의 codec/profile/해상도가 push 원본과 동일하면 OK.
# READ-ONLY 변형: 통제된 push 대신, push된 원본의 무변환 사본인 RTMP 재배포(계약 3)와
#   HLS 출력을 ffprobe로 비교한다. 두 경로가 codec_name/profile/width/height 동일하면
#   remux-only(트랜스코딩 없음)가 성립.
set -u

KEY="${STREAM_KEY:-$(docker exec sentinel-cctv-adapter wget -qO- http://streaming:8080/api/streams | jq -r '[.[]|select(.active)][0].streamKey // empty')}"
if [ -z "$KEY" ]; then echo "NOK: no active stream to observe"; exit 1; fi
echo "observing streamKey=$KEY"

PROBE="-v error -select_streams v:0 -show_entries stream=codec_name,profile,width,height -of csv=p=0"
HLS=$(timeout 60 docker exec sentinel-cctv-adapter ffprobe $PROBE "http://streaming:8080/live/${KEY}/index.m3u8" 2>&1 | tail -1)
RTMP=$(timeout 60 docker exec sentinel-cctv-adapter ffprobe $PROBE "rtmp://streaming:1935/live/${KEY}" 2>&1 | tail -1)
echo "HLS : $HLS"
echo "RTMP: $RTMP"

if [ -n "$HLS" ] && [ "$HLS" = "$RTMP" ] && printf '%s' "$HLS" | grep -q '^h264'; then
  echo "OK: HLS == RTMP source relay (codec/profile/resolution identical, h264)"
  exit 0
fi
echo "NOK: HLS output differs from pushed source (transcoding suspected) or probe failed"
exit 1
