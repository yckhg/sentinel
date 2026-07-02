#!/usr/bin/env bash
# 계약7-3. user 토큰으로 POST /api/test-alert → 403
# spec: docs/spec/interface-web-api.md 계약 7
# SKIP: mutating(test-alert 발사 위험) + fixture 부재(user 토큰).
set -uo pipefail; . "$(dirname "$0")/../lib-web.sh"
[ -n "${USER_TOKEN:-}" ] || skip "(fixture 부재): USER_TOKEN env 필요"
require_mutating
code=$(bcode -X POST -H "Authorization: Bearer $USER_TOKEN" "$BACKEND/api/test-alert")
echo "code=$code"
[ "$code" = "403" ] && ok "403" || nok "기대 403, 관측 $code (403 아니면 테스트 알람이 실발사됐을 수 있음)"
