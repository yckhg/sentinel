#!/usr/bin/env bash
# 계약15-7. GET /api/equipment/status → 200 배열, 모든 항목 alertState ∈ {none, active}
# spec: docs/spec/interface-web-api.md 계약 15
set -uo pipefail; . "$(dirname "$0")/../lib-web.sh"
out=$(bcurl_code "$HWGW/api/equipment/status")
code=$(echo "$out" | tail -1); body=$(echo "$out" | head -n -1)
echo "code=$code"; echo "$body" | jq -c . 2>/dev/null | head -c 400; echo
[ "$code" = "200" ] || nok "기대 200, 관측 $code"
echo "$body" | jq -e 'type=="array"' >/dev/null || nok "배열 아님"
n=$(echo "$body" | jq 'length')
[ "$n" = "0" ] && ok "200 + 빈 배열 (인메모리 상태 — 항목 shape는 항목 존재 시 검증)" || true
echo "$body" | jq -e 'all(.[]; .alertState=="none" or .alertState=="active")' >/dev/null \
  && ok "200 + ${n}항목 alertState 범위 일치" || nok "alertState 범위 밖"
