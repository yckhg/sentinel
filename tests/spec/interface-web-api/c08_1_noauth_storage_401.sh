#!/usr/bin/env bash
# 계약8-1. JWT 없이 GET /api/storage → 401 (프록시 이전 차단)
# spec: docs/spec/interface-web-api.md 계약 8
set -uo pipefail; . "$(dirname "$0")/../lib-web.sh"
code=$(bcode "$BACKEND/api/storage")
echo "code=$code"
[ "$code" = "401" ] && ok "401" || nok "기대 401, 관측 $code"
