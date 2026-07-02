#!/usr/bin/env bash
# A2-1 (playlist 형식/헤더): push 중인 스트림의 m3u8 응답이
#   HTTP 200 + Content-Type: application/vnd.apple.mpegurl + Cache-Control: no-cache
#   + Access-Control-Allow-Origin: * + 본문 첫 줄 #EXTM3U 이면 OK.
# READ-ONLY: 기존 라이브 스트림에 대한 GET 관찰만 수행.
set -u

KEY="${STREAM_KEY:-$(docker exec sentinel-cctv-adapter wget -qO- http://streaming:8080/api/streams | jq -r '[.[]|select(.active)][0].streamKey // empty')}"
if [ -z "$KEY" ]; then echo "NOK: no active stream to observe"; exit 1; fi
echo "observing streamKey=$KEY"

OUT=$(docker exec sentinel-cctv-adapter wget -S -qO- "http://streaming:8080/live/${KEY}/index.m3u8" 2>&1)
echo "--- response (headers+body head) ---"
printf '%s\n' "$OUT" | head -20

FAIL=0
printf '%s\n' "$OUT" | grep -q 'HTTP/1.1 200' || { echo "FAIL: not HTTP 200"; FAIL=1; }
printf '%s\n' "$OUT" | grep -qi 'Content-Type: application/vnd.apple.mpegurl' || { echo "FAIL: Content-Type mismatch"; FAIL=1; }
printf '%s\n' "$OUT" | grep -qi 'Cache-Control: no-cache' || { echo "FAIL: Cache-Control missing"; FAIL=1; }
printf '%s\n' "$OUT" | grep -qi 'Access-Control-Allow-Origin: \*' || { echo "FAIL: CORS header missing"; FAIL=1; }
BODY_FIRST=$(printf '%s\n' "$OUT" | grep -v '^  ' | head -1)
[ "$BODY_FIRST" = "#EXTM3U" ] || { echo "FAIL: body does not start with #EXTM3U (got: $BODY_FIRST)"; FAIL=1; }

[ "$FAIL" -eq 0 ] && { echo "OK"; exit 0; }
echo "NOK"; exit 1
