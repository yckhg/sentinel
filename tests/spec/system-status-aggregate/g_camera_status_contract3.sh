#!/usr/bin/env bash
# G. 카메라 연결 상태 관측 — 계약 3 GET /api/cameras 의 status ∈ {connected,disconnected}.
#    (집계 응답 shape 밖 — 요약 창은 카메라 상태를 별도 조회한다.)
# spec: docs/spec/system-status-aggregate.md — 단언 G / 계약 3(불변)
# read-only.
set -uo pipefail; . "$(dirname "$0")/../lib-web.sh"
T=$(get_token) || skip "(fixture 부재): admin 토큰 획득 실패"

out=$(bcurl_code -H "Authorization: Bearer $T" "$BACKEND/api/cameras")
code=$(printf '%s' "$out" | tail -n1); body=$(printf '%s' "$out" | sed '$d')
echo "code=$code"; printf '%s' "$body" | jq -c '[.[] | {id, name, status}]' 2>/dev/null | head -c 400; echo
[ "$code" = "200" ] || nok "GET /api/cameras 기대 200, 관측 $code"
printf '%s' "$body" | jq -e 'type=="array"' >/dev/null || nok "배열 아님"

n=$(printf '%s' "$body" | jq 'length')
[ "$n" -ge 1 ] || skip "(fixture 부재): 등록된 카메라 없음"

# 각 카메라가 status 필드를 가지며 값이 connected|disconnected 어휘 안이다.
printf '%s' "$body" | jq -e 'all(.[]; .status=="connected" or .status=="disconnected")' >/dev/null \
  && ok "${n}대 카메라 status 전부 connected|disconnected (계약 3)" \
  || nok "status 어휘(connected|disconnected) 밖 항목 존재"
