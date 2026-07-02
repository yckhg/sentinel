#!/usr/bin/env bash
# D. 가입 승인 상태 기계 — register(pending) → login 403 → approve → login 200, DB status 전이
# spec: docs/spec/web-backend.md — 검증 단언 (TDD)
# SKIP: mutating — 계정 생성 + 승인 + 로그인 반복.
set -uo pipefail; . "$(dirname "$0")/../lib-web.sh"
require_mutating
T=$(get_token) || exit 1
U="spectdd-d-$(date +%s)"
reg=$(bcurl -X POST "$BACKEND/auth/register" -H 'Content-Type: application/json' \
  -d "{\"username\":\"$U\",\"password\":\"secret123\",\"confirmPassword\":\"secret123\",\"name\":\"x\"}")
id=$(echo "$reg" | jq -r .id); st1=$(echo "$reg" | jq -r .status)
c1=$(bcode -X POST "$BACKEND/auth/login" -H 'Content-Type: application/json' -d "{\"username\":\"$U\",\"password\":\"secret123\"}")
dbst1=$(db_query "SELECT status FROM users WHERE username='$U'")
bcurl -X POST -H "Authorization: Bearer $T" "$BACKEND/auth/approve/$id" >/dev/null
c2=$(bcode -X POST "$BACKEND/auth/login" -H 'Content-Type: application/json' -d "{\"username\":\"$U\",\"password\":\"secret123\"}")
dbst2=$(db_query "SELECT status FROM users WHERE username='$U'")
echo "register=$st1 login1=$c1 db1=$dbst1 login2=$c2 db2=$dbst2"
[ "$st1" = "pending" ] && [ "$c1" = "403" ] && [ "$c2" = "200" ] && [ "$dbst1" = "pending" ] && [ "$dbst2" = "active" ] \
  && ok "상태 기계 일치" || nok "전이 불일치"
