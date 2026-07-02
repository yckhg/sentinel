#!/usr/bin/env bash
# A2-2 (세그먼트 길이): 키프레임 간격 요구(≤2초)를 지키는 push에서
#   playlist #EXTINF 값이 2.0±0.5초, 세그먼트 수 ≤ 6 이면 OK.
# READ-ONLY: 기존 라이브 스트림 playlist 관찰. (원 단언은 A1-1 테스트 스트림 대상 —
#   여기서는 키프레임 간격을 만족하는 기존 push(yt-cam-*: -g 60 재인코딩)에 적용)
set -u

KEY="${STREAM_KEY:-$(docker exec sentinel-cctv-adapter wget -qO- http://streaming:8080/api/streams | jq -r '[.[]|select(.active)][0].streamKey // empty')}"
if [ -z "$KEY" ]; then echo "NOK: no active stream to observe"; exit 1; fi
echo "observing streamKey=$KEY"

M3U8=$(docker exec sentinel-cctv-adapter wget -qO- "http://streaming:8080/live/${KEY}/index.m3u8")
echo "--- playlist ---"; printf '%s\n' "$M3U8"

FAIL=0
COUNT=$(printf '%s\n' "$M3U8" | grep -c '^#EXTINF')
[ "$COUNT" -ge 1 ] || { echo "FAIL: no EXTINF entries"; FAIL=1; }
[ "$COUNT" -le 6 ] || { echo "FAIL: segment count $COUNT > 6"; FAIL=1; }
while read -r DUR; do
  awk -v d="$DUR" 'BEGIN{exit !(d>=1.5 && d<=2.5)}' || { echo "FAIL: EXTINF $DUR out of 2.0±0.5"; FAIL=1; }
done < <(printf '%s\n' "$M3U8" | sed -n 's/^#EXTINF:\([0-9.]*\),.*/\1/p')

echo "segments=$COUNT"
[ "$FAIL" -eq 0 ] && { echo "OK"; exit 0; }
echo "NOK"; exit 1
