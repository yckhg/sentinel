#!/usr/bin/env bash
# 계약9-1. admin으로 임시 링크 생성 → 201, url에 /view/<token> 포함
# spec: docs/spec/interface-web-api.md 계약 9
# SKIP: mutating — 24시간 유효한 실제 열람 토큰(보안 아티팩트)을 발급함.
set -uo pipefail; . "$(dirname "$0")/../lib-web.sh"
require_mutating
T=$(get_token) || exit 1
out=$(bcurl_code -X POST -H "Authorization: Bearer $T" -H 'Content-Type: application/json' -d '{"label":"spec-tdd"}' "$BACKEND/api/links/temp")
code=$(echo "$out" | tail -1); body=$(echo "$out" | head -n -1)
echo "code=$code"; echo "$body" | jq -c '{id, expiresAt}'
id=$(echo "$body" | jq -r .id); tok=$(echo "$body" | jq -r .token)
match=$(echo "$body" | jq -r .url | grep -c "/view/$tok" || true)
bcurl -X DELETE -H "Authorization: Bearer $T" "$BACKEND/api/links/$id" >/dev/null  # cleanup(회수)
[ "$code" = "201" ] && [ "$match" = "1" ] && ok "201 + /view/<token>" || nok "code=$code url매치=$match"
