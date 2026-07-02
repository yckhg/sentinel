#!/usr/bin/env bash
# 계약10-1. 초대 생성 → 201, token 64자 hex
# spec: docs/spec/interface-web-api.md 계약 10
# SKIP: mutating — 실제 초대 생성 + notifier 경유 이메일 발송 시도 유발.
set -uo pipefail; . "$(dirname "$0")/../lib-web.sh"
require_mutating
T=$(get_token) || exit 1
out=$(bcurl_code -X POST -H "Authorization: Bearer $T" -H 'Content-Type: application/json' \
  -d '{"email":"spectdd@example.invalid"}' "$BACKEND/api/invitations")
code=$(echo "$out" | tail -1); body=$(echo "$out" | head -n -1)
echo "code=$code"; echo "$body" | jq -c '{id, status}'
id=$(echo "$body" | jq -r .id)
hex=$(echo "$body" | jq -e '.token | test("^[0-9a-f]{64}$")' >/dev/null && echo 1 || echo 0)
bcurl -X DELETE -H "Authorization: Bearer $T" "$BACKEND/api/invitations/$id" >/dev/null  # cleanup(취소)
[ "$code" = "201" ] && [ "$hex" = "1" ] && ok "201 + 64hex" || nok "code=$code hex=$hex"
