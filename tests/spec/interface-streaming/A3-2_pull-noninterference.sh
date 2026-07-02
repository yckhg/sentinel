#!/usr/bin/env bash
# A3-2 (소비자 무간섭): RTMP pull 세션을 붙였다 떼어도 같은 streamKey의
#   HLS playlist 갱신(mtime 전진)과 /api/streams의 active:true 가 유지되면 OK.
# READ-ONLY: pull 구독은 읽기 전용 관찰 (push/HLS/상태에 영향 없음이 곧 검증 대상).
set -u

KEY="${STREAM_KEY:-$(docker exec sentinel-cctv-adapter wget -qO- http://streaming:8080/api/streams | jq -r '[.[]|select(.active)][0].streamKey // empty')}"
if [ -z "$KEY" ]; then echo "NOK: no active stream to observe"; exit 1; fi
echo "observing streamKey=$KEY"

M0=$(docker exec sentinel-streaming stat -c %Y "/tmp/hls/${KEY}/index.m3u8")
A0=$(docker exec sentinel-cctv-adapter wget -qO- http://streaming:8080/api/streams | jq -r ".[]|select(.streamKey==\"$KEY\").active")
echo "before pull: mtime=$M0 active=$A0"

# RTMP pull 세션 attach (~8초 읽기) 후 detach
timeout 20 docker exec sentinel-cctv-adapter ffmpeg -v error -t 8 \
  -i "rtmp://streaming:1935/live/${KEY}" -f null - >/dev/null 2>&1
echo "pull session attached 8s and detached (rc=$?)"

sleep 6
M1=$(docker exec sentinel-streaming stat -c %Y "/tmp/hls/${KEY}/index.m3u8")
A1=$(docker exec sentinel-cctv-adapter wget -qO- http://streaming:8080/api/streams | jq -r ".[]|select(.streamKey==\"$KEY\").active")
echo "after pull: mtime=$M1 active=$A1"

if [ "$A0" = "true" ] && [ "$A1" = "true" ] && [ "$M1" -gt "$M0" ]; then
  echo "OK: playlist kept updating and active:true held across pull attach/detach"
  exit 0
fi
echo "NOK: HLS update or active state disturbed by RTMP pull session"
exit 1
