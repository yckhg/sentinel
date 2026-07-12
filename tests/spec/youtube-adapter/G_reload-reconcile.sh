#!/usr/bin/env bash
# SKIP: mutating — POST /api/cameras/reload 는 프로덕션 어댑터의 소스 집합을 web-backend
#   목록으로 전면 교체함 (스펙 리뷰 필요 1: config 기반 localFile 스트림 전부 중지 위험).
#   web-backend 다운 시나리오(4)는 web-backend 중지 필요 (설계자 승인 대기).
# youtube-adapter §단언 G (reload 재조정): (1) 200 {"status":"ok","count":N} + 목록 일치
#   (2) 비대상(youtube 아님/disabled/빈 streamKey) 제외 (3) 동일 카메라 startedAt 불변
#   (4) web-backend 다운 시 500 + 기존 스트림 유지.
set -u

# 게이트 변수 일관화: SPEC_TDD_ALLOW_MUTATING(스트리밍군 정본) 또는 ALLOW_MUTATING 중
#   하나라도 1 이면 youtube 게이트군(C/F/G/J/J-2)이 함께 켜진다(조용한 부분초록 방지).
if [ "${SPEC_TDD_ALLOW_MUTATING:-0}" != "1" ] && [ "${ALLOW_MUTATING:-0}" != "1" ]; then
  echo "SKIPPED: POST reload replaces production source set (would stop localFile streams). Set SPEC_TDD_ALLOW_MUTATING=1 to run."
  exit 2
fi

BEFORE=$(docker exec sentinel-youtube-adapter wget -qO- http://localhost:8080/api/streams/status)
echo "before: $BEFORE"
OUT=$(docker exec sentinel-youtube-adapter wget -qO- --post-data='' http://localhost:8080/api/cameras/reload)
echo "reload: $OUT"
printf '%s' "$OUT" | jq -e '.status=="ok" and (.count|type=="number")' >/dev/null || { echo "NOK: bad reload response"; exit 1; }
N=$(printf '%s' "$OUT" | jq '.count')
AFTER=$(docker exec sentinel-youtube-adapter wget -qO- http://localhost:8080/api/streams/status)
echo "after: $AFTER"
printf '%s' "$AFTER" | jq -e "length == $N" >/dev/null \
  && { echo "OK (scenarios 2-4 require web-backend fixtures/down-state — run separately)"; exit 0; }
echo "NOK: status list length != count"
exit 1
