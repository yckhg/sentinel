#!/usr/bin/env bash
# 계약5-2. 잘못된 phone으로 POST → 400
# spec: docs/spec/interface-web-api.md 계약 5
# SKIP: mutating — POST /api/contacts (구현 오류 시 실제 연락처 생성 → 알림 수신자 오염).
set -uo pipefail; . "$(dirname "$0")/../lib-web.sh"
require_mutating
T=$(get_token) || exit 1
code=$(bcode -X POST -H "Authorization: Bearer $T" -H 'Content-Type: application/json' \
  -d '{"name":"spectdd","phone":"1234","email":"","notifyEmail":false}' "$BACKEND/api/contacts")
echo "code=$code"
[ "$code" = "400" ] && ok "400" || nok "기대 400, 관측 $code"
