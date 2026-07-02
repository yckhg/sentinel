#!/usr/bin/env bash
# 계약2-7. resolve 성공 직후 WS 클라이언트가 incident_resolved 수신 (계약 14 교차)
# spec: docs/spec/interface-web-api.md 계약 2
# SKIP: mutating — 실제 incident resolve 필요.
set -uo pipefail; . "$(dirname "$0")/../lib-web.sh"
require_mutating
T=$(get_token) || exit 1
id=$(bcurl -H "Authorization: Bearer $T" "$BACKEND/api/incidents?status=open&limit=1" | jq -r '.data[0].id // empty')
[ -n "$id" ] || skip "(fixture 부재): open incident 없음"
wslog="$SPEC_TMP/c02_7_ws.log"
ws_observe "/ws?token=$T" 15 normal > "$wslog" &
wpid=$!; sleep 3
bcurl -X PATCH -H "Authorization: Bearer $T" -H 'Content-Type: application/json' \
  -d '{"resolutionNotes":"spec-tdd ws broadcast"}' "$BACKEND/api/incidents/$id/resolve" >/dev/null
wait $wpid; cat "$wslog"
grep -q '"type":"incident_resolved"' "$wslog" && ok "incident_resolved 수신" || nok "incident_resolved 미수신"
