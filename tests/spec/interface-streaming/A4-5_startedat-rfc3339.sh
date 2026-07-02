#!/usr/bin/env bash
# A4-5 (startedAt 형식): 모든 항목의 startedAt 이 RFC3339 UTC(...Z)로 파싱되면 OK.
# READ-ONLY: GET /api/streams 관찰.
set -u

BODY=$(docker exec sentinel-cctv-adapter wget -qO- http://streaming:8080/api/streams)
echo "body: $BODY"
printf '%s' "$BODY" | jq -e 'all(.[]; (.startedAt | test("^\\d{4}-\\d{2}-\\d{2}T\\d{2}:\\d{2}:\\d{2}(\\.\\d+)?Z$")) and ((.startedAt | sub("\\.\\d+Z$";"Z") | fromdateiso8601) > 0))' \
  && { echo "OK"; exit 0; }
echo "NOK: startedAt is not RFC3339 UTC"
exit 1
