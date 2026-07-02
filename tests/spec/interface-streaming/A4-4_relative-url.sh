#!/usr/bin/env bash
# A4-4 (상대 URL 정책): 모든 항목의 hlsUrl 이 ^/live/[^/]+/index\.m3u8$ 에 매치하고
#   'http' 를 포함하지 않으면 OK.
# READ-ONLY: GET /api/streams 관찰.
set -u

BODY=$(docker exec sentinel-cctv-adapter wget -qO- http://streaming:8080/api/streams)
echo "body: $BODY"
printf '%s' "$BODY" | jq -e 'all(.[]; (.hlsUrl | test("^/live/[^/]+/index\\.m3u8$")) and ((.hlsUrl | contains("http")) | not))' \
  && { echo "OK"; exit 0; }
echo "NOK: hlsUrl violates relative URL policy"
exit 1
