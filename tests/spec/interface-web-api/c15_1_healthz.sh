#!/usr/bin/env bash
# 계약15-1. hw-gateway GET /healthz → 200 {"status":"ok","service":"hw-gateway"}
# spec: docs/spec/interface-web-api.md 계약 15
set -uo pipefail; . "$(dirname "$0")/../lib-web.sh"
out=$(bcurl_code "$HWGW/healthz")
code=$(echo "$out" | tail -1); body=$(echo "$out" | head -n -1)
echo "code=$code body=$body"
[ "$code" = "200" ] || nok "기대 200, 관측 $code"
echo "$body" | jq -e '.status=="ok" and .service=="hw-gateway"' >/dev/null && ok "hw-gateway healthz 일치" || nok "바디 불일치"
