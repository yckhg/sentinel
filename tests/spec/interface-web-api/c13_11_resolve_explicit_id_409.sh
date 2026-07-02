#!/usr/bin/env bash
# 계약13-11. 이미 resolved인 incident의 명시적 id로 resolve-from-sensor 재전송 → 409
# spec: docs/spec/interface-web-api.md 계약 13
# SKIP: mutating — resolve 선행 필요 (c13_10 후속).
set -uo pipefail; . "$(dirname "$0")/../lib-web.sh"
require_mutating
id=$(db_query "SELECT id FROM incidents WHERE site_id='spectdd' AND status='resolved' ORDER BY id DESC LIMIT 1")
[ -n "$id" ] || skip "(fixture 부재): spectdd resolved incident 없음 — c13_10 선행 필요"
code=$(bcode -X POST -H 'Content-Type: application/json' \
  -d "{\"incidentId\":$id,\"siteId\":\"spectdd\",\"resolvedBy\":{\"kind\":\"sensor_button\",\"id\":\"SPEC-BTN\",\"label\":\"spec\"}}" \
  "$BACKEND/api/incidents/$id/resolve-from-sensor")
echo "code=$code"
[ "$code" = "409" ] && ok "중복 버튼 방어 409" || nok "기대 409, 관측 $code"
