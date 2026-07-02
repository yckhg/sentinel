#!/usr/bin/env bash
# A3-1 (pull 가능): push 중 rtmp://streaming:1935/live/{streamKey} 를 ffprobe로 pull 하면
#   스트림 정보(h264 등)가 반환되면 OK.
# READ-ONLY: RTMP subscriber로 잠깐 붙는 관찰 (recording 서비스와 동일 경로).
set -u

KEY="${STREAM_KEY:-$(docker exec sentinel-cctv-adapter wget -qO- http://streaming:8080/api/streams | jq -r '[.[]|select(.active)][0].streamKey // empty')}"
if [ -z "$KEY" ]; then echo "NOK: no active stream to observe"; exit 1; fi
echo "observing streamKey=$KEY"

OUT=$(timeout 60 docker exec sentinel-cctv-adapter ffprobe -v error \
  -show_entries stream=codec_name -of csv=p=0 "rtmp://streaming:1935/live/${KEY}" 2>&1)
RC=$?
echo "--- ffprobe(rtmp) ---"; echo "$OUT"

if [ "$RC" -eq 0 ] && printf '%s\n' "$OUT" | grep -q 'h264'; then
  echo "OK: RTMP pull returned stream info (h264)"
  exit 0
fi
echo "NOK: RTMP pull failed (rc=$RC)"
exit 1
