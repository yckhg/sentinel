#!/usr/bin/env bash
# J. 센서 해소 fallback — 미해결 2건 중 최신만 resolved, kind=sensor_button, WS incident_resolved
# spec: docs/spec/web-backend.md — 검증 단언 (TDD)
# SKIP: mutating — incident 생성/해소.
# 격리: run-고유 siteId 로 다른 게이트/부하가 남긴 open 사고와 섞이지 않게 한다
#       (resolve-from-sensor 는 해당 site 최신 미해결을 해소하므로 site 공유 시 위양성).
#       이 테스트는 open 상태의 j-old 를 매 run 남기므로(하니스 DB 사본은 read-only →
#       cleanup 불가), site 격리가 유일한 무오염 수단이다. ns 타임스탬프 + PID + RANDOM
#       로 같은-초 재실행에서도 site 가 절대 겹치지 않게 하여 old_still_open 누적을 차단.
set -uo pipefail; . "$(dirname "$0")/../lib-web.sh"
require_mutating
T=$(get_token) || exit 1
SID="spectdd-j-$(date +%s%N)-$$-${RANDOM}"
bcurl -X POST -H 'Content-Type: application/json' -d "{\"siteId\":\"$SID\",\"description\":\"j-old\",\"isTest\":true}" "$BACKEND/api/incidents" >/dev/null
sleep 1
new=$(bcurl -X POST -H 'Content-Type: application/json' -d "{\"siteId\":\"$SID\",\"description\":\"j-new\",\"isTest\":true}" "$BACKEND/api/incidents" | jq -r .id)
wslog="$SPEC_TMP/j_ws.log"; ws_observe "/ws?token=$T" 12 normal > "$wslog" & wpid=$!; sleep 3
bcurl -X POST -H 'Content-Type: application/json' -d "{\"incidentId\":0,\"siteId\":\"$SID\",\"resolvedBy\":{\"label\":\"L\"}}" "$BACKEND/api/incidents/0/resolve-from-sensor" >/dev/null
wait $wpid
row=$(db_query "SELECT status, resolved_by_kind FROM incidents WHERE id=$new")
old_open=$(db_query "SELECT COUNT(*) FROM incidents WHERE site_id='$SID' AND status='open' AND description='j-old'")
nres=$(grep -c '"type":"incident_resolved"' "$wslog" || true)
echo "site=$SID newest=$row old_still_open=$old_open ws=$nres"
[ "$row" = "resolved|sensor_button" ] && [ "$old_open" = "1" ] && [ "$nres" -ge 1 ] && ok "최신만 해소 + attribution + WS" || nok "불일치"
