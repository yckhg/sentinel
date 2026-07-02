#!/usr/bin/env bash
# 계약15-3. 브로커 정상 시 POST /api/test-alert {} → 200 {"status":"sent","topic":"safety/test/alert"}
# spec: docs/spec/interface-web-api.md 계약 15
# SKIP: mutating — 실제 테스트 알람 발사 (incident 생성 + 알림 파이프라인 전체 가동).
set -uo pipefail; . "$(dirname "$0")/../lib-web.sh"
require_mutating
out=$(bcurl_code -X POST -H 'Content-Type: application/json' -d '{}' "$HWGW/api/test-alert")
code=$(echo "$out" | tail -1); body=$(echo "$out" | head -n -1)
echo "code=$code body=$body"
[ "$code" = "200" ] && echo "$body" | jq -e '.status=="sent" and .topic=="safety/test/alert"' >/dev/null \
  && ok "200 + topic 일치" || nok "code=$code body=$body"
