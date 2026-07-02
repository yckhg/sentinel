#!/usr/bin/env bash
# 계약13-7. POST /api/incidents (siteId 있음) → 201 + WS crisis_alert 도착
# spec: docs/spec/interface-web-api.md 계약 13 (계약 14 교차)
# SKIP: mutating — 실제 incident 생성 + 전 클라이언트 위기 배너 발생 + notifier 알림 발송 위험.
set -uo pipefail; . "$(dirname "$0")/../lib-web.sh"
require_mutating
T=$(get_token) || exit 1
wslog="$SPEC_TMP/c13_07_ws.log"
ws_observe "/ws?token=$T" 15 normal > "$wslog" &
wpid=$!; sleep 3
out=$(bcurl_code -X POST -H 'Content-Type: application/json' \
  -d '{"siteId":"spectdd","description":"spec-tdd crisis","isTest":true}' "$BACKEND/api/incidents")
code=$(echo "$out" | tail -1)
wait $wpid; cat "$wslog"
[ "$code" = "201" ] && grep -q '"type":"crisis_alert"' "$wslog" && ok "201 + crisis_alert 수신" || nok "code=$code / 브로드캐스트 확인 실패"
