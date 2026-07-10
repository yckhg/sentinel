#!/usr/bin/env bash
# V. null-alertId 공존 — spec: docs/spec/web-backend.md 단언 T (부분 UNIQUE 인덱스 회귀 게이트)
#   alertId 없는 incident 2건 POST → 둘 다 201·서로 다른 id·둘 다 persisted.
#   idx_incidents_alert_id 가 `WHERE alert_id IS NOT NULL` 부분 인덱스임을 보장한다:
#   장차 마이그레이션이 이 인덱스를 전체 UNIQUE(alert_id)로 넓히면 NULL alertId 둘째
#   건이 UNIQUE 위반(500)으로 붕괴하므로, 이 게이트가 단언 T 를 그 회귀로부터 보호한다.
#   (append-only: 기존 테스트 단언은 건드리지 않고 신규 케이스만 추가.)
# SKIP: mutating — 실제 incident 2건 생성.
# 격리: run-고유 siteId(alertId 가 없어 site 로 격리·판정). admin 인증 불필요
#       (POST /api/incidents 는 Docker 내부용 unauth 경로 — H/T 게이트와 동일).
set -uo pipefail; . "$(dirname "$0")/../lib-web.sh"

# read-only 보조(항상 수행): 부분 인덱스가 alert_id IS NOT NULL 로 한정되어 있는지 확인.
idx=$(db_query "SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND name='idx_incidents_alert_id' AND sql LIKE '%alert_id IS NOT NULL%'")
echo "INFO: alert_id 부분 인덱스(WHERE alert_id IS NOT NULL) 존재=$idx (1이어야 함)"
[ "$idx" = "1" ] || nok "부분 인덱스가 alert_id IS NOT NULL 로 한정되지 않음 — null-alertId 붕괴 위험"

require_mutating

SID="spectdd-v-$(date +%s)-$$"   # run-고유 siteId
BODY="{\"siteId\":\"$SID\",\"description\":\"v\",\"isTest\":true}"   # alertId 의도적 생략(NULL)

# --- per-run cleanup (판정 후 EXIT 훅) : 이 실행이 만든 run-태그 행만 삭제 ----------------
#   db_query 는 볼륨을 :ro 로 마운트해 삭제 불가 → 여기서 :rw 로 마운트해 DELETE.
#   ok/nok 판정 이후 trap 으로 실행되어 verdict 에 영향 없음. best-effort(실패는 경고만).
cleanup_run() {
  local b a
  b=$(db_query "SELECT COUNT(*) FROM incidents WHERE site_id='$SID'" 2>/dev/null)
  docker run --rm -v sentinel_db-data:/data "$PY_IMG" python -c '
import sqlite3, sys
con = sqlite3.connect("/data/sentinel.db", timeout=20)
con.execute("PRAGMA busy_timeout=20000")
for stmt in sys.argv[1:]:
    try: con.execute(stmt)
    except Exception as e: sys.stderr.write("cleanup DELETE warn: %s\n" % e)
con.commit(); con.close()
' "DELETE FROM incidents WHERE site_id='$SID'" 2>&1 | sed 's/^/  cleanup-warn: /' || true
  a=$(db_query "SELECT COUNT(*) FROM incidents WHERE site_id='$SID'" 2>/dev/null)
  echo "  cleanup(incidents site_id=$SID): ${b:-?}->${a:-?}"
}
trap cleanup_run EXIT

o1=$(bcurl_code -X POST -H 'Content-Type: application/json' -d "$BODY" "$BACKEND/api/incidents")
o2=$(bcurl_code -X POST -H 'Content-Type: application/json' -d "$BODY" "$BACKEND/api/incidents")
c1=$(echo "$o1" | tail -1); id1=$(echo "$o1" | head -n -1 | jq -r '.id // empty')
c2=$(echo "$o2" | tail -1); id2=$(echo "$o2" | head -n -1 | jq -r '.id // empty')

persisted=$(db_query "SELECT COUNT(*) FROM incidents WHERE site_id='$SID' AND alert_id IS NULL")
echo "null-alertId: 1st=$c1/$id1 2nd=$c2/$id2 persisted(site,alert_id NULL)=$persisted"

# 판정: 두 건 모두 201 + id 존재 + 서로 다른 id(붕괴 아님) + 둘 다 persisted.
[ "$c1" = "201" ] && [ "$c2" = "201" ] \
  && [ -n "$id1" ] && [ -n "$id2" ] && [ "$id1" != "$id2" ] \
  && [ "$persisted" = "2" ] \
  && ok "null-alertId 공존 (2건 201·distinct id·persisted 2)" \
  || nok "null-alertId 공존 위반 (c1=$c1 c2=$c2 id1=$id1 id2=$id2 persisted=$persisted)"
