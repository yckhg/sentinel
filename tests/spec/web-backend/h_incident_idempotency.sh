#!/usr/bin/env bash
# H. alertId 멱등 — 동일 alertId 2회 POST → 201/200 동일 id, COUNT==1
# spec: docs/spec/web-backend.md — 검증 단언 (TDD)
# SKIP: mutating — 실제 incident 생성. (read-only 보조: unique 부분 인덱스 존재 확인은 항상 수행)
set -uo pipefail; . "$(dirname "$0")/../lib-web.sh"
idx=$(db_query "SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND name='idx_incidents_alert_id' AND sql LIKE '%UNIQUE%alert_id IS NOT NULL%'")
echo "INFO: alert_id UNIQUE 부분 인덱스 존재=$idx (1이어야 함)"
[ "$idx" = "1" ] || nok "unique 부분 인덱스 부재 — DB 레벨 멱등 보강 없음"
require_mutating
AID="spectdd-h-$(date +%s)"
o1=$(bcurl_code -X POST -H 'Content-Type: application/json' -d "{\"siteId\":\"spectdd\",\"description\":\"h\",\"isTest\":true,\"alertId\":\"$AID\"}" "$BACKEND/api/incidents")
o2=$(bcurl_code -X POST -H 'Content-Type: application/json' -d "{\"siteId\":\"spectdd\",\"description\":\"h\",\"isTest\":true,\"alertId\":\"$AID\"}" "$BACKEND/api/incidents")
c1=$(echo "$o1" | tail -1); id1=$(echo "$o1" | head -n -1 | jq .id)
c2=$(echo "$o2" | tail -1); id2=$(echo "$o2" | head -n -1 | jq .id)
n=$(db_query "SELECT COUNT(*) FROM incidents WHERE alert_id='$AID'")
echo "1st=$c1/$id1 2nd=$c2/$id2 count=$n"
[ "$c1" = "201" ] && [ "$c2" = "200" ] && [ "$id1" = "$id2" ] && [ "$n" = "1" ] && ok "멱등" || nok "멱등 위반"
