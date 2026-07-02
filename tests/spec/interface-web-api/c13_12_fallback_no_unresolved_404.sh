#!/usr/bin/env bash
# 계약13-12. 미해결 없음 상태에서 fallback(0/0) 재전송 → 404 (409 아님)
# spec: docs/spec/interface-web-api.md 계약 13
# SKIP: mutating — 대상 site의 미해결 소진 전제 필요 (미해결이 남아 있으면 실사고를 해소해버림).
set -uo pipefail; . "$(dirname "$0")/../lib-web.sh"
require_mutating
n=$(db_query "SELECT COUNT(*) FROM incidents WHERE site_id='spectdd' AND status != 'resolved'")
[ "$n" = "0" ] || skip "(전제 불충족): spectdd 미해결 ${n}건 존재 — 오발 해소 방지 위해 중단"
out=$(bcurl_code -X POST -H 'Content-Type: application/json' \
  -d '{"incidentId":0,"siteId":"spectdd","resolvedBy":{"kind":"sensor_button","id":"SPEC-BTN","label":"spec"}}' \
  "$BACKEND/api/incidents/0/resolve-from-sensor")
code=$(echo "$out" | tail -1); body=$(echo "$out" | head -n -1)
echo "code=$code body=$body"
[ "$code" = "404" ] && ok "404 (409 아님)" || nok "기대 404, 관측 $code"
