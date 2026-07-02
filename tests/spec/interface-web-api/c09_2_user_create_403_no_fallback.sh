#!/usr/bin/env bash
# 계약9-2. user 토큰으로 생성 → 403 (internal 폴백 아님)
# spec: docs/spec/interface-web-api.md 계약 9
# SKIP: mutating + fixture 부재(user 토큰).
set -uo pipefail; . "$(dirname "$0")/../lib-web.sh"
[ -n "${USER_TOKEN:-}" ] || skip "(fixture 부재): USER_TOKEN env 필요"
require_mutating
code=$(bcode -X POST -H "Authorization: Bearer $USER_TOKEN" -H 'Content-Type: application/json' -d '{}' "$BACKEND/api/links/temp")
echo "code=$code"
[ "$code" = "403" ] && ok "403 (폴백 없음)" || nok "기대 403, 관측 $code"
