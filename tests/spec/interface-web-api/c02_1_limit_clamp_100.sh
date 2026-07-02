#!/usr/bin/env bash
# 계약2-1. GET /api/incidents?limit=500 → 200, pagination.limit == 100 (클램프)
# spec: docs/spec/interface-web-api.md 계약 2
set -uo pipefail; . "$(dirname "$0")/../lib-web.sh"
T=$(get_token) || skip "(fixture 부재): admin 토큰 획득 실패"
out=$(bcurl -H "Authorization: Bearer $T" "$BACKEND/api/incidents?limit=500")
echo "$out" | jq -c .pagination
echo "$out" | jq -e '.pagination.limit == 100' >/dev/null && ok "limit 100으로 클램프" || nok "pagination.limit != 100"
