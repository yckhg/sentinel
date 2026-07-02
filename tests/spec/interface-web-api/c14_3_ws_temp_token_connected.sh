#!/usr/bin/env bash
# 계약14-3. temp link 토큰으로 접속 → 연결 성립, 첫 메시지 connected (role 식별은 ⚠️리뷰 2)
# spec: docs/spec/interface-web-api.md 계약 14
# SKIP: mutating — temp 링크 발급(보안 아티팩트 생성)이 선행돼야 함.
set -uo pipefail; . "$(dirname "$0")/../lib-web.sh"
require_mutating
T=$(get_token) || exit 1
body=$(bcurl -X POST -H "Authorization: Bearer $T" -H 'Content-Type: application/json' -d '{"label":"spec-tdd-ws"}' "$BACKEND/api/links/temp")
tok=$(echo "$body" | jq -r .token); id=$(echo "$body" | jq -r .id)
out=$(ws_observe "/ws?token=$tok" 6 normal)
bcurl -X DELETE -H "Authorization: Bearer $T" "$BACKEND/api/links/$id" >/dev/null  # cleanup
echo "$out" | head -3
echo "$out" | grep -q '^HTTP: HTTP/1.1 101' || nok "연결 미성립"
echo "$out" | grep '^TEXT: ' | head -1 | sed 's/^TEXT: //' | jq -e '.type=="connected"' >/dev/null \
  && ok "temp 토큰 접속 + connected" || nok "connected 미수신"
