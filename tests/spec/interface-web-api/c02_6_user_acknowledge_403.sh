#!/usr/bin/env bash
# 계약2-6. user 토큰으로 acknowledge → 403
# spec: docs/spec/interface-web-api.md 계약 2
# SKIP: mutating(PATCH) + fixture 부재(user 토큰). USER_TOKEN + ALLOW_MUTATING=1 시 실행.
set -uo pipefail; . "$(dirname "$0")/../lib-web.sh"
[ -n "${USER_TOKEN:-}" ] || skip "(fixture 부재): USER_TOKEN env 필요"
require_mutating
T=$(get_token) || exit 1
id=$(bcurl -H "Authorization: Bearer $T" "$BACKEND/api/incidents?status=open&limit=1" | jq -r '.data[0].id // empty')
[ -n "$id" ] || skip "(fixture 부재): open incident 없음"
code=$(bcode -X PATCH -H "Authorization: Bearer $USER_TOKEN" "$BACKEND/api/incidents/$id/acknowledge")
echo "code=$code"
[ "$code" = "403" ] && ok "user acknowledge 403" || nok "기대 403, 관측 $code"
