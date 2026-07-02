#!/usr/bin/env bash
# J. 센서 해소 fallback — 미해결 2건 중 최신만 resolved, kind=sensor_button, WS incident_resolved
# spec: docs/spec/web-backend.md — 검증 단언 (TDD)
# SKIP: mutating — incident 생성/해소.
set -uo pipefail; . "$(dirname "$0")/../lib-web.sh"
require_mutating
T=$(get_token) || exit 1
bcurl -X POST -H 'Content-Type: application/json' -d '{"siteId":"spectdd","description":"j-old","isTest":true}' "$BACKEND/api/incidents" >/dev/null
sleep 1
new=$(bcurl -X POST -H 'Content-Type: application/json' -d '{"siteId":"spectdd","description":"j-new","isTest":true}' "$BACKEND/api/incidents" | jq -r .id)
wslog="$SPEC_TMP/j_ws.log"; ws_observe "/ws?token=$T" 12 normal > "$wslog" & wpid=$!; sleep 3
bcurl -X POST -H 'Content-Type: application/json' -d '{"incidentId":0,"siteId":"spectdd","resolvedBy":{"label":"L"}}' "$BACKEND/api/incidents/0/resolve-from-sensor" >/dev/null
wait $wpid
row=$(db_query "SELECT status, resolved_by_kind FROM incidents WHERE id=$new")
old_open=$(db_query "SELECT COUNT(*) FROM incidents WHERE site_id='spectdd' AND status='open' AND description='j-old'")
nres=$(grep -c '"type":"incident_resolved"' "$wslog" || true)
echo "newest=$row old_still_open=$old_open ws=$nres"
[ "$row" = "resolved|sensor_button" ] && [ "$old_open" = "1" ] && [ "$nres" -ge 1 ] && ok "최신만 해소 + attribution + WS" || nok "불일치"
