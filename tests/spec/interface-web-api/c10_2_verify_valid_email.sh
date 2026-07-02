#!/usr/bin/env bash
# 계약10-2. verify(유효 초대) → 200, email 일치
# spec: docs/spec/interface-web-api.md 계약 10
# SKIP: fixture 부재 — 현재 DB에 유효(pending·미만료) 초대 없음(실측: 전부 cancelled).
#       유효 초대 생성은 mutating이므로 설계자 승인 대기.
set -uo pipefail; . "$(dirname "$0")/../lib-web.sh"
row=$(db_query "SELECT token,email FROM invitations WHERE status='pending' AND expires_at > datetime('now') LIMIT 1")
[ -n "$row" ] || skip "(fixture 부재): 유효 pending 초대 없음"
tok=${row%%|*}; email=${row##*|}
out=$(bcurl "$BACKEND/api/invitations/verify/$tok"); echo "$out"
echo "$out" | jq -e --arg e "$email" '.email == $e and .status == "valid"' >/dev/null \
  && ok "email 일치 + valid" || nok "verify 응답 불일치"
