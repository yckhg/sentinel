#!/usr/bin/env bash
# E. GET /api/incidents 응답의 모든 data[].status 가 {open, resolved} 안에 든다(acknowledged 부재).
# spec: docs/spec/alarm-history-lifecycle.md — 단언 E
# read-only. 마이그레이션 적용된 DB 대상. vacuous 방지 위해 최소 1건 존재를 확인.
set -uo pipefail; . "$(dirname "$0")/../lib-web.sh"
T=$(get_token) || skip "(fixture 부재): admin 토큰 획득 실패"
body=$(bcurl -H "Authorization: Bearer $T" "$BACKEND/api/incidents?limit=100")
n=$(printf '%s' "$body" | jq '.data|length' 2>/dev/null)
echo "count=$n"
[ -n "$n" ] || nok "응답 파싱 실패: $body"
[ "$n" != "0" ] || skip "(vacuous 방지): incidents 비어있음 — 최소 1건 시딩 필요"
printf '%s' "$body" | jq -e 'all(.data[]; .status=="open" or .status=="resolved")' >/dev/null \
  && ok "상태집합 ⊆ {open, resolved}" || nok "acknowledged 등 계약외 상태 존재: $(printf '%s' "$body" | jq -c '[.data[].status]|unique')"
