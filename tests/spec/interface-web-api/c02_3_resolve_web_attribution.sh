#!/usr/bin/env bash
# 계약2-3. open incident resolve(notes 있음) → 200, resolvedByKind=="web"
# spec: docs/spec/interface-web-api.md 계약 2
# SKIP: mutating — 실제 미해결 사고를 resolve 처리함 (incident 상태 변경 + MQTT/WS 부작용).
set -uo pipefail; . "$(dirname "$0")/../lib-web.sh"
require_mutating
T=$(get_token) || exit 1
id=$(bcurl -H "Authorization: Bearer $T" "$BACKEND/api/incidents?status=open&limit=1" | jq -r '.data[0].id // empty')
[ -n "$id" ] || skip "(fixture 부재): open incident 없음"
out=$(bcurl -X PATCH -H "Authorization: Bearer $T" -H 'Content-Type: application/json' \
  -d '{"resolutionNotes":"spec-tdd resolve"}' "$BACKEND/api/incidents/$id/resolve")
echo "$out" | jq -c .
[ "$(echo "$out" | jq -r .resolvedByKind)" = "web" ] && ok "resolvedByKind=web" || nok "attribution 불일치"
