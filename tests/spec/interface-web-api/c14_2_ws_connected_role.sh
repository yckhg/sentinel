#!/usr/bin/env bash
# 계약14-2. 유효 JWT 접속 → 첫 메시지 type=="connected", payload.role이 토큰 role과 일치
# spec: docs/spec/interface-web-api.md 계약 14
set -uo pipefail; . "$(dirname "$0")/../lib-web.sh"
T=$(get_token) || skip "(fixture 부재): admin 토큰 획득 실패"
role=$(echo "$T" | cut -d. -f2 | tr '_-' '/+' | { p=$(cat); pad=$(( (4 - ${#p} % 4) % 4 )); printf '%s' "$p"; printf '=%.0s' $(seq 1 $pad) 2>/dev/null; } | base64 -d 2>/dev/null | jq -r .role)
out=$(ws_observe "/ws?token=$T" 6 normal)
first=$(echo "$out" | grep '^TEXT: ' | head -1 | sed 's/^TEXT: //')
echo "token role=$role"; echo "first=$first"
[ -n "$first" ] || nok "텍스트 프레임 미수신"
echo "$first" | jq -e '.type == "connected"' >/dev/null || nok "첫 메시지가 connected 아님"
[ "$(echo "$first" | jq -r .payload.role)" = "$role" ] && ok "connected + role=$role 일치" || nok "role 불일치"
