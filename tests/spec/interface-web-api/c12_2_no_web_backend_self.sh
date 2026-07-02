#!/usr/bin/env bash
# 계약12-2. /api/health 응답에 id=="web-backend" service 항목 없음 (자기 자신 제외)
# spec: docs/spec/interface-web-api.md 계약 12
set -uo pipefail; . "$(dirname "$0")/../lib-web.sh"
T=$(get_token) || skip "(fixture 부재): admin 토큰 획득 실패"
n=$(bcurl -H "Authorization: Bearer $T" "$BACKEND/api/health" | jq '[.[] | select(.kind=="service" and .id=="web-backend")] | length')
echo "web-backend entries=$n"
[ "$n" = "0" ] && ok "자기 자신 미포함" || nok "web-backend 항목 존재"
