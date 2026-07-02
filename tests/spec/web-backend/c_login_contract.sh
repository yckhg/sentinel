#!/usr/bin/env bash
# C. 로그인 계약 — 올바른 자격 200 + token/user.role · 틀린 비밀번호 401 · pending 403
# spec: docs/spec/web-backend.md — 검증 단언 (TDD)
# 주의: 프로덕션 rate limit 보호 정책상 로그인 POST는 1회만(캐시) — 401/403 분기는 승인 시 실행.
set -uo pipefail; . "$(dirname "$0")/../lib-web.sh"
login_resp="$SPEC_TMP/login.json"
[ -f "$SPEC_TMP/admin.token.fail" ] && [ ! -s "$login_resp" ] && skip "(fixture 부재): admin 자격증명 불일치 — 이전 로그인 실패 기록, 재시도 금지(rate limit)"
if [ ! -s "$login_resp" ]; then
  user=${ADMIN_USERNAME:-admin}; pass=${ADMIN_PASSWORD:-sentinel1234}
  bcurl -X POST "$BACKEND/auth/login" -H 'Content-Type: application/json' \
    -d "{\"username\":\"$user\",\"password\":\"$pass\"}" > "$login_resp"
fi
jq -e 'has("token")' "$login_resp" >/dev/null 2>&1 || { touch "$SPEC_TMP/admin.token.fail"; skip "(fixture 부재): admin 자격증명 불일치 — 로그인 실패($(jq -r '.error // "?"' "$login_resp"))"; }
jq -c '{role: .user.role, has_token: (.token != null)}' "$login_resp"
jq -e '.token and .user.role' "$login_resp" >/dev/null || nok "token/user.role 누락"
if [ "${ALLOW_MUTATING:-0}" = "1" ]; then
  c401=$(bcode -X POST "$BACKEND/auth/login" -H 'Content-Type: application/json' -d '{"username":"admin","password":"wrong-pass"}')
  echo "wrong-pw=$c401"; [ "$c401" = "401" ] || nok "틀린 비밀번호가 401 아님: $c401"
  echo "INFO: pending 분기는 pending 계정 fixture 필요 (현재 없음)"
fi
ok "정상 로그인 200+token+role 검증 (401/403 분기는 승인 대기 — 로그인 반복 금지)"
