#!/usr/bin/env bash
# 계약14-9. 회수(DELETE)된 temp 토큰의 /ws 업그레이드 → 접속 시점 401 (blacklist 확인, 연결 미수립).
# spec: docs/spec/interface-web-api.md 계약 14 (회수 temp 토큰 업그레이드 401)
# SKIP: mutating — temp 링크 발급 후 회수 필요. ALLOW_MUTATING=1 로만.
set -uo pipefail; . "$(dirname "$0")/../lib-web.sh"
require_mutating
T=$(get_token) || exit 1
body=$(bcurl -X POST -H "Authorization: Bearer $T" -H 'Content-Type: application/json' -d '{"label":"spec-c14_9"}' "$BACKEND/api/links/temp")
tok=$(echo "$body" | jq -r .token); id=$(echo "$body" | jq -r .id)
{ [ -n "$tok" ] && [ "$tok" != "null" ]; } || nok "temp 링크 발급 실패"
bcurl -X DELETE -H "Authorization: Bearer $T" "$BACKEND/api/links/$id" >/dev/null   # 회수
out=$(ws_observe "/ws?token=$tok" 5 normal)
line=$(echo "$out" | grep '^HTTP: ' | head -1)
echo "$line"
if echo "$line" | grep -q '101'; then nok "회수 temp 토큰 WS 업그레이드가 101 로 성립 (401 이어야 함 — 접속시점 blacklist 미확인)"; fi
echo "$line" | grep -q '401' && ok "회수 temp 토큰 WS 업그레이드 401" || nok "업그레이드 응답이 401 아님: $line"
