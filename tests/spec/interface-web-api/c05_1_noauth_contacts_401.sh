#!/usr/bin/env bash
# 계약5-1. JWT 없이 GET /api/contacts → 401
# spec: docs/spec/interface-web-api.md 계약 5
set -uo pipefail; . "$(dirname "$0")/../lib-web.sh"
code=$(bcode "$BACKEND/api/contacts")
echo "code=$code"
[ "$code" = "401" ] && ok "401" || nok "기대 401, 관측 $code"
