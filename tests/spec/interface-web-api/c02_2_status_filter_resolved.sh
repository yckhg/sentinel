#!/usr/bin/env bash
# 계약2-2. status=resolved 필터 → data[] 전부 status=="resolved"
# spec: docs/spec/interface-web-api.md 계약 2
set -uo pipefail; . "$(dirname "$0")/../lib-web.sh"
T=$(get_token) || skip "(fixture 부재): admin 토큰 획득 실패"
out=$(bcurl -H "Authorization: Bearer $T" "$BACKEND/api/incidents?status=resolved&limit=100")
n=$(echo "$out" | jq '.data | length'); echo "resolved rows=$n"
[ "$n" -ge 1 ] || skip "(fixture 부재): resolved incident 없음"
echo "$out" | jq -e 'all(.data[]; .status == "resolved")' >/dev/null \
  && ok "$n건 모두 resolved" || nok "resolved 아닌 항목 존재"
