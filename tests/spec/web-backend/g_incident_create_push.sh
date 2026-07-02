#!/usr/bin/env bash
# G. 사고 생성+push — 무인증 POST /api/incidents → 201, DB open, WS crisis_alert 1건
# spec: docs/spec/web-backend.md — 검증 단언 (TDD)
# SKIP: mutating — 실제 incident 생성 + 전 클라이언트 위기 배너.
set -uo pipefail; . "$(dirname "$0")/../lib-web.sh"
require_mutating
T=$(get_token) || exit 1
wslog="$SPEC_TMP/g_ws.log"; ws_observe "/ws?token=$T" 15 normal > "$wslog" & wpid=$!; sleep 3
out=$(bcurl_code -X POST -H 'Content-Type: application/json' -d '{"siteId":"spectdd","description":"spec-g","isTest":true}' "$BACKEND/api/incidents")
code=$(echo "$out" | tail -1); id=$(echo "$out" | head -n -1 | jq -r .id)
st=$(db_query "SELECT status FROM incidents WHERE id=$id")
wait $wpid
nmsg=$(grep -c '"type":"crisis_alert"' "$wslog" || true)
echo "code=$code db.status=$st crisis_alert=$nmsg"
[ "$code" = "201" ] && [ "$st" = "open" ] && [ "$nmsg" -ge 1 ] && ok "생성+push" || nok "불일치"
