#!/usr/bin/env bash
# B. 인증 관문 — 헤더 없음 401 / Bearer $T 200
# spec: docs/spec/web-backend.md — 검증 단언 (TDD)
set -uo pipefail; . "$(dirname "$0")/../lib-web.sh"
c1=$(bcode "$BACKEND/api/cameras")
T=$(get_token) || skip "(fixture 부재): admin 토큰 획득 실패"
c2=$(bcode -H "Authorization: Bearer $T" "$BACKEND/api/cameras")
echo "noauth=$c1 auth=$c2"
[ "$c1" = "401" ] && [ "$c2" = "200" ] && ok "401 → 200" || nok "noauth=$c1 auth=$c2"
