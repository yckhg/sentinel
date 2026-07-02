#!/usr/bin/env bash
# 계약12-3. /api/health/events?entity_kind=sensor → 모든 entityKind=="sensor"
# spec: docs/spec/interface-web-api.md 계약 12
set -uo pipefail; . "$(dirname "$0")/../lib-web.sh"
T=$(get_token) || skip "(fixture 부재): admin 토큰 획득 실패"
out=$(bcurl -H "Authorization: Bearer $T" "$BACKEND/api/health/events?entity_kind=sensor&limit=50")
n=$(echo "$out" | jq 'length'); echo "rows=$n"
[ "$n" -ge 1 ] || skip "(fixture 부재): sensor 이벤트 없음"
echo "$out" | jq -e 'all(.[]; .entityKind == "sensor")' >/dev/null && ok "${n}행 모두 sensor" || nok "sensor 아닌 행 존재"
