#!/usr/bin/env bash
# 계약13-2. GET /internal/contacts (무인증) → 200 JSON 배열 (계약 5 스키마)
# spec: docs/spec/interface-web-api.md 계약 13
set -uo pipefail; . "$(dirname "$0")/../lib-web.sh"
out=$(bcurl_code "$BACKEND/internal/contacts")
code=$(echo "$out" | tail -1); body=$(echo "$out" | head -n -1)
echo "code=$code rows=$(echo "$body" | jq 'length' 2>/dev/null)"
[ "$code" = "200" ] || nok "기대 200, 관측 $code"
echo "$body" | jq -e 'type=="array" and all(.[]; has("id") and has("name") and has("phone") and has("notifyEmail"))' >/dev/null \
  && ok "무인증 200 + 계약5 스키마" || nok "스키마 불일치"
