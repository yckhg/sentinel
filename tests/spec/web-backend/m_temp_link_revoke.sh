#!/usr/bin/env bash
# M. 임시 링크 폐기 — 발급→verify 200→DELETE 204→verify 401
# spec: docs/spec/web-backend.md — 검증 단언 (TDD)
# SKIP: mutating — 실제 열람 토큰 발급/회수.
set -uo pipefail; . "$(dirname "$0")/../lib-web.sh"
require_mutating
T=$(get_token) || exit 1
body=$(bcurl -X POST -H "Authorization: Bearer $T" -H 'Content-Type: application/json' -d '{"label":"spec-m"}' "$BACKEND/api/links/temp")
id=$(echo "$body" | jq -r .id); tok=$(echo "$body" | jq -r .token)
v1=$(bcode "$BACKEND/api/links/verify/$tok")
d=$(bcode -X DELETE -H "Authorization: Bearer $T" "$BACKEND/api/links/$id")
v2=$(bcode "$BACKEND/api/links/verify/$tok")
echo "verify1=$v1 delete=$d verify2=$v2"
[ "$v1" = "200" ] && [ "$d" = "204" ] && [ "$v2" = "401" ] && ok "폐기 체인 일치" || nok "불일치"
