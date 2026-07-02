#!/usr/bin/env bash
# streaming §단언 E (상대 URL 정책): 모든 hlsUrl 이 /live/ 로 시작, index.m3u8 로 끝나고
#   스킴/호스트명 미포함. 각 항목 cameraId == streamKey.
# READ-ONLY.
set -u

BODY=$(docker exec sentinel-cctv-adapter wget -qO- http://streaming:8080/api/streams)
echo "body: $BODY"
printf '%s' "$BODY" | jq -e 'all(.[]; (.hlsUrl | startswith("/live/")) and (.hlsUrl | endswith("index.m3u8")) and ((.hlsUrl | test("https?://")) | not) and (.cameraId == .streamKey))' \
  && { echo "OK"; exit 0; }
echo "NOK: relative URL policy or cameraId==streamKey violated"
exit 1
