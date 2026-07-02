#!/usr/bin/env bash
# 계약13-8. POST /api/incidents 동일 alertId 2회 → 1회차 201, 2회차 200 + 동일 id (행 1개)
# spec: docs/spec/interface-web-api.md 계약 13
# SKIP: mutating — 실제 incident 생성.
set -uo pipefail; . "$(dirname "$0")/../lib-web.sh"
require_mutating
AID="spectdd-dup-$(date +%s)"
o1=$(bcurl_code -X POST -H 'Content-Type: application/json' -d "{\"siteId\":\"spectdd\",\"description\":\"t\",\"isTest\":true,\"alertId\":\"$AID\"}" "$BACKEND/api/incidents")
o2=$(bcurl_code -X POST -H 'Content-Type: application/json' -d "{\"siteId\":\"spectdd\",\"description\":\"t\",\"isTest\":true,\"alertId\":\"$AID\"}" "$BACKEND/api/incidents")
c1=$(echo "$o1" | tail -1); id1=$(echo "$o1" | head -n -1 | jq .id)
c2=$(echo "$o2" | tail -1); id2=$(echo "$o2" | head -n -1 | jq .id)
n=$(db_query "SELECT COUNT(*) FROM incidents WHERE alert_id='$AID'")
echo "1st=$c1/$id1 2nd=$c2/$id2 rows=$n"
[ "$c1" = "201" ] && [ "$c2" = "200" ] && [ "$id1" = "$id2" ] && [ "$n" = "1" ] && ok "alertId dedup 멱등" || nok "멱등 위반"
