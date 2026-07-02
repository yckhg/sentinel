#!/usr/bin/env bash
# 계약9-3. 발급 직후 verify → 200 {"valid":true}
# spec: docs/spec/interface-web-api.md 계약 9
# SKIP: mutating — 검증하려면 실제 링크 발급이 선행돼야 함 (현재 활성 링크 fixture 없음).
set -uo pipefail; . "$(dirname "$0")/../lib-web.sh"
require_mutating
T=$(get_token) || exit 1
body=$(bcurl -X POST -H "Authorization: Bearer $T" -H 'Content-Type: application/json' -d '{"label":"spec-tdd-verify"}' "$BACKEND/api/links/temp")
id=$(echo "$body" | jq -r .id); tok=$(echo "$body" | jq -r .token)
out=$(bcurl "$BACKEND/api/links/verify/$tok"); echo "$out"
bcurl -X DELETE -H "Authorization: Bearer $T" "$BACKEND/api/links/$id" >/dev/null  # cleanup
echo "$out" | jq -e '.valid == true' >/dev/null && ok "valid:true" || nok "valid 아님"
