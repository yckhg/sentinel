#!/usr/bin/env bash
# A4-5 (lastUpdatedAt 형식·의미): 모든 항목의 lastUpdatedAt 이 RFC3339 UTC(...Z)로 파싱되고,
#   응답에 옛 이름 startedAt 키가 존재하지 않으면 OK (startedAt -> lastUpdatedAt 개명 완료 확인).
# READ-ONLY: GET /api/streams 관찰.
# vacuity: 항목 수 n 을 함께 보고한다 — n==0 이면 all(empty;...) 이 공허하게 통과하므로,
#   개명(startedAt 부재)을 non-vacuous 하게 실증하려면 활성 스트림이 있어야 한다.
#   비공허를 기계적으로 강제한다: n==0 이면 exit 2(SKIPPED) — 공허 green 금지.
set -u

BODY=$(docker exec sentinel-cctv-adapter wget -qO- http://streaming:8080/api/streams)
echo "body: $BODY"
N=$(printf '%s' "$BODY" | jq -r 'length' 2>/dev/null || echo "?")
echo "sample_n=$N"

# 비공허 강제: 관찰 항목이 없으면 개명을 실증할 수 없으므로 SKIPPED (공허 통과 금지)
if [ "$N" = "0" ]; then
  echo "SKIPPED: no active streams (n==0) — 개명(startedAt 부재)을 non-vacuous 하게 실증 불가. 활성 스트림 하에서 재실행 필요."
  exit 2
fi
if [ "$N" = "?" ]; then
  echo "NOK: /api/streams 응답을 파싱할 수 없음"
  exit 1
fi

printf '%s' "$BODY" | jq -e 'all(.[]; (.lastUpdatedAt | test("^\\d{4}-\\d{2}-\\d{2}T\\d{2}:\\d{2}:\\d{2}(\\.\\d+)?Z$")) and (has("startedAt") | not))' \
  && { echo "OK (non-vacuous; sample_n=$N)"; exit 0; }
echo "NOK: lastUpdatedAt 형식 위반 또는 옛 이름 startedAt 잔존"
exit 1
