#!/usr/bin/env bash
# A. 무인증 헬스체크 → 200 {"status":"ok","service":"web-backend"}
# spec: docs/spec/web-backend.md — 검증 단언 (TDD)
set -uo pipefail; . "$(dirname "$0")/../lib-web.sh"
out=$(bcurl_code "$BACKEND/healthz")
code=$(echo "$out" | tail -1); body=$(echo "$out" | head -n -1)
echo "code=$code body=$body"
[ "$code" = "200" ] && echo "$body" | jq -e '.status=="ok" and .service=="web-backend"' >/dev/null \
  && ok "healthz 계약 일치" || nok "code=$code body=$body"
