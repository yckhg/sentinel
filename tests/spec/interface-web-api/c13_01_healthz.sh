#!/usr/bin/env bash
# 계약13-1. GET /healthz (무인증) → 200 {"status":"ok","service":"web-backend"}
# spec: docs/spec/interface-web-api.md 계약 13
set -uo pipefail; . "$(dirname "$0")/../lib-web.sh"
out=$(bcurl_code "$BACKEND/healthz")
code=$(echo "$out" | tail -1); body=$(echo "$out" | head -n -1)
echo "code=$code body=$body"
[ "$code" = "200" ] || nok "기대 200, 관측 $code"
echo "$body" | jq -e '.status=="ok" and .service=="web-backend"' >/dev/null && ok "healthz 계약 일치" || nok "바디 불일치"
