#!/usr/bin/env bash
# 계약9-5. DELETE 후 GET /api/links 목록에서 제외
# spec: docs/spec/interface-web-api.md 계약 9
# SKIP: mutating — 링크 발급/회수 필요.
set -uo pipefail; . "$(dirname "$0")/../lib-web.sh"
require_mutating
T=$(get_token) || exit 1
id=$(bcurl -X POST -H "Authorization: Bearer $T" -H 'Content-Type: application/json' -d '{"label":"spec-tdd-list"}' "$BACKEND/api/links/temp" | jq -r .id)
bcurl -X DELETE -H "Authorization: Bearer $T" "$BACKEND/api/links/$id" >/dev/null
n=$(bcurl -H "Authorization: Bearer $T" "$BACKEND/api/links" | jq "[.[] | select(.id==\"$id\")] | length")
echo "remaining=$n"
[ "$n" = "0" ] && ok "목록 제외" || nok "회수 링크가 목록에 남음"
