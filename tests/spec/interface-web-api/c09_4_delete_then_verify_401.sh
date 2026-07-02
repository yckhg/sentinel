#!/usr/bin/env bash
# 계약9-4. DELETE(회수) 후 같은 token verify → 401
# spec: docs/spec/interface-web-api.md 계약 9
# SKIP: mutating — 링크 발급/회수 필요.
set -uo pipefail; . "$(dirname "$0")/../lib-web.sh"
require_mutating
T=$(get_token) || exit 1
body=$(bcurl -X POST -H "Authorization: Bearer $T" -H 'Content-Type: application/json' -d '{"label":"spec-tdd-revoke"}' "$BACKEND/api/links/temp")
id=$(echo "$body" | jq -r .id); tok=$(echo "$body" | jq -r .token)
d=$(bcode -X DELETE -H "Authorization: Bearer $T" "$BACKEND/api/links/$id")
v=$(bcode "$BACKEND/api/links/verify/$tok")
echo "delete=$d verify=$v"
[ "$d" = "204" ] && [ "$v" = "401" ] && ok "회수 후 verify 401" || nok "delete=$d verify=$v"
