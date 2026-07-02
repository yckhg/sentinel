#!/usr/bin/env bash
# 공통-2. user 토큰으로 admin 전용 GET /api/settings → 403
# spec: docs/spec/interface-web-api.md — 공통 검증 단언
# SKIP: fixture 부재 — user role 계정의 자격증명이 config/env에 없음 (로그인 1회 제한 정책).
#       USER_TOKEN env로 user JWT를 주입하면 실행된다.
set -uo pipefail; . "$(dirname "$0")/../lib-web.sh"
[ -n "${USER_TOKEN:-}" ] || skip "(fixture 부재): USER_TOKEN env 필요"
code=$(bcode -H "Authorization: Bearer $USER_TOKEN" "$BACKEND/api/settings")
echo "code=$code"
[ "$code" = "403" ] && ok "user → settings 403" || nok "기대 403, 관측 $code"
