#!/usr/bin/env bash
# 계약6-1. user 토큰으로 GET /api/devices/all → 403
# spec: docs/spec/interface-web-api.md 계약 6
# SKIP: fixture 부재 — user 토큰 없음. USER_TOKEN env 주입 시 실행.
set -uo pipefail; . "$(dirname "$0")/../lib-web.sh"
[ -n "${USER_TOKEN:-}" ] || skip "(fixture 부재): USER_TOKEN env 필요"
code=$(bcode -H "Authorization: Bearer $USER_TOKEN" "$BACKEND/api/devices/all")
echo "code=$code"
[ "$code" = "403" ] && ok "403" || nok "기대 403, 관측 $code"
