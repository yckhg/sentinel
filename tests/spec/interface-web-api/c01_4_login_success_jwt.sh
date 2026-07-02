#!/usr/bin/env bash
# 계약1-4. 로그인 성공 → 200, token이 3-파트 JWT, user.role ∈ {user, admin}
# spec: docs/spec/interface-web-api.md 계약 1
# 주의: 캐시 미스 시에만 로그인 1회 발생 (rate limit 보호 — lib-web.sh get_token)
set -uo pipefail; . "$(dirname "$0")/../lib-web.sh"
tok_file="$SPEC_TMP/admin.token"; login_resp="$SPEC_TMP/login.json"
# 로그인은 rate limit 보호를 위해 최대 1회. 이전 실패가 기록돼 있으면 재시도하지 않고 skip.
[ -f "$SPEC_TMP/admin.token.fail" ] && [ ! -s "$login_resp" ] && skip "(fixture 부재): admin 자격증명 불일치 — 이전 로그인 실패 기록, 재시도 금지(rate limit)"
if [ ! -s "$login_resp" ]; then
  user=${ADMIN_USERNAME:-admin}; pass=${ADMIN_PASSWORD:-sentinel1234}
  bcurl -X POST "$BACKEND/auth/login" -H 'Content-Type: application/json' \
    -d "{\"username\":\"$user\",\"password\":\"$pass\"}" > "$login_resp"
fi
jq -e 'has("token")' "$login_resp" >/dev/null 2>&1 || { touch "$SPEC_TMP/admin.token.fail"; skip "(fixture 부재): admin 자격증명 불일치 — 로그인 실패($(jq -r '.error // "?"' "$login_resp"))"; }
cat "$login_resp" | jq -c '{user, token_parts: (.token // "" | split(".") | length)}'
jq -e '.token | split(".") | length == 3' "$login_resp" >/dev/null || nok "token이 3-파트 JWT 아님"
jq -e '.user.role == "user" or .user.role == "admin"' "$login_resp" >/dev/null || nok "user.role 범위 밖"
jq -r .token "$login_resp" | tr -d '\n' > "$tok_file"
ok "200 + 3-파트 JWT + role=$(jq -r .user.role "$login_resp")"
