#!/usr/bin/env bash
# 공통-1. JWT 없이 GET /api/cameras → 401 + {"error": ...}
# spec: docs/spec/interface-web-api.md — 공통 검증 단언
set -uo pipefail; . "$(dirname "$0")/../lib-web.sh"
out=$(bcurl_code "$BACKEND/api/cameras")
code=$(echo "$out" | tail -1); body=$(echo "$out" | head -n -1)
echo "code=$code body=$body"
[ "$code" = "401" ] || nok "기대 401, 관측 $code"
echo "$body" | jq -e 'has("error")' >/dev/null || nok "에러 봉투 {\"error\":...} 아님"
ok "401 + error 봉투"
