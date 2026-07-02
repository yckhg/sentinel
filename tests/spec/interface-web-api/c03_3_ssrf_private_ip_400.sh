#!/usr/bin/env bash
# 계약3-3. sourceUrl private IP(rtsp://192.168.0.10/stream) 생성 → 400 (SSRF 차단)
# spec: docs/spec/interface-web-api.md 계약 3
# SKIP: mutating — POST /api/cameras.
set -uo pipefail; . "$(dirname "$0")/../lib-web.sh"
require_mutating
T=$(get_token) || exit 1
code=$(bcode -X POST -H "Authorization: Bearer $T" -H 'Content-Type: application/json' \
  -d '{"name":"spectdd-ssrf","sourceType":"rtsp","sourceUrl":"rtsp://192.168.0.10/stream","enabled":false}' "$BACKEND/api/cameras")
echo "code=$code"
[ "$code" = "400" ] && ok "SSRF 차단 400" || nok "기대 400, 관측 $code"
