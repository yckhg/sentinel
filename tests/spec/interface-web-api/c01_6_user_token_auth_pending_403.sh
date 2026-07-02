#!/usr/bin/env bash
# 계약1-6. user 토큰으로 GET /auth/pending → 401 또는 403 (200 아님)
# spec: docs/spec/interface-web-api.md 계약 1
# SKIP: fixture 부재 — user role 자격증명 없음. USER_TOKEN env 주입 시 실행.
set -uo pipefail; . "$(dirname "$0")/../lib-web.sh"
[ -n "${USER_TOKEN:-}" ] || skip "(fixture 부재): USER_TOKEN env 필요"
code=$(bcode -H "Authorization: Bearer $USER_TOKEN" "$BACKEND/auth/pending")
echo "code=$code"
{ [ "$code" = "401" ] || [ "$code" = "403" ]; } && ok "$code" || nok "기대 401/403, 관측 $code"
