#!/usr/bin/env bash
# 계약1-3. pending 계정 로그인 → 403 + error에 pending 명시
# spec: docs/spec/interface-web-api.md 계약 1
# SKIP: fixture 부재 — 현재 DB에 pending 계정 없음(실측) + 로그인 반복 금지 정책.
#       PENDING_USER/PENDING_PASS env 주입 시 실행.
set -uo pipefail; . "$(dirname "$0")/../lib-web.sh"
[ -n "${PENDING_USER:-}" ] && [ -n "${PENDING_PASS:-}" ] || skip "(fixture 부재): pending 계정 자격 필요"
out=$(bcurl_code -X POST "$BACKEND/auth/login" -H 'Content-Type: application/json' \
  -d "{\"username\":\"$PENDING_USER\",\"password\":\"$PENDING_PASS\"}")
code=$(echo "$out" | tail -1); body=$(echo "$out" | head -n -1); echo "code=$code body=$body"
[ "$code" = "403" ] || nok "기대 403, 관측 $code"
echo "$body" | jq -r .error | grep -qi pending && ok "403 + pending 명시" || nok "error에 pending 미명시"
