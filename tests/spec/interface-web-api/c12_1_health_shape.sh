#!/usr/bin/env bash
# 계약12-1. GET /api/health — 모든 항목 kind ∈ {service,sensor}, status ∈ {healthy,unhealthy}
# spec: docs/spec/interface-web-api.md 계약 12
set -uo pipefail; . "$(dirname "$0")/../lib-web.sh"
T=$(get_token) || skip "(fixture 부재): admin 토큰 획득 실패"
out=$(bcurl -H "Authorization: Bearer $T" "$BACKEND/api/health")
echo "$out" | jq -c '[.[] | {kind, id, status}]'
n=$(echo "$out" | jq 'length'); [ "$n" -ge 1 ] || nok "health 항목 0개"
echo "$out" | jq -e 'all(.[]; (.kind=="service" or .kind=="sensor") and (.status=="healthy" or .status=="unhealthy"))' >/dev/null \
  && ok "${n}개 항목 shape 일치" || nok "kind/status 범위 밖 항목 존재"
