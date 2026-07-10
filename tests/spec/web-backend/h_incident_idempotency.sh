#!/usr/bin/env bash
# H. alertId 멱등 (동시성 포함) — spec: docs/spec/web-backend.md 단언 H
#   (1) 순차 2회 POST → 201/200 동일 id, COUNT==1  (기존 계약, 유지)
#   (2) 같은 alertId N(>=12) 동시 POST → 정확히 1건 201·나머지 200 동일 id,
#       5xx/SQLITE_BUSY/UNIQUE 위반 0건, COUNT==1  (동시성 원자성 강화)
# SKIP: mutating — 실제 incident 생성. (read-only 보조: unique 부분 인덱스 존재 확인은 항상 수행)
set -uo pipefail; . "$(dirname "$0")/../lib-web.sh"

idx=$(db_query "SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND name='idx_incidents_alert_id' AND sql LIKE '%UNIQUE%alert_id IS NOT NULL%'")
echo "INFO: alert_id UNIQUE 부분 인덱스 존재=$idx (1이어야 함)"
[ "$idx" = "1" ] || nok "unique 부분 인덱스 부재 — DB 레벨 멱등 보강 없음"
require_mutating

# 격리: run-고유 siteId (판정은 run-고유 alertId 로 이미 격리되나 site 도 고유화).
SID="spectdd-h-$(date +%s)-$$"

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

# --- (1) 순차 2회 (기존 계약) --------------------------------------------------
AID="${SID}-a"
o1=$(bcurl_code -X POST -H 'Content-Type: application/json' -d "{\"siteId\":\"$SID\",\"description\":\"h\",\"isTest\":true,\"alertId\":\"$AID\"}" "$BACKEND/api/incidents")
o2=$(bcurl_code -X POST -H 'Content-Type: application/json' -d "{\"siteId\":\"$SID\",\"description\":\"h\",\"isTest\":true,\"alertId\":\"$AID\"}" "$BACKEND/api/incidents")
c1=$(echo "$o1" | tail -1); id1=$(echo "$o1" | head -n -1 | jq .id)
c2=$(echo "$o2" | tail -1); id2=$(echo "$o2" | head -n -1 | jq .id)
n=$(db_query "SELECT COUNT(*) FROM incidents WHERE alert_id='$AID'")
echo "seq: 1st=$c1/$id1 2nd=$c2/$id2 count=$n"
[ "$c1" = "201" ] && [ "$c2" = "200" ] && [ "$id1" = "$id2" ] && [ "$n" = "1" ] || nok "순차 멱등 위반"

# --- (2) N 동시 (동시성 원자성) ------------------------------------------------
N=${H_CONCURRENCY:-12}
AID2="${SID}-c"
BODY2="{\"siteId\":\"$SID\",\"description\":\"hc\",\"isTest\":true,\"alertId\":\"$AID2\"}"

# 한 컨테이너 안에서 N개 curl을 백그라운드로 동시 발사 → 라인당 "<code> <body>" 출력.
lines=$(docker run --rm --network "$NET" --entrypoint sh "$CURL_IMG" -c '
  N="$1"; BODY="$2"; URL="$3"; i=1
  while [ "$i" -le "$N" ]; do
    ( curl -s -o "/tmp/b.$i" -w "%{http_code}" -X POST \
        -H "Content-Type: application/json" -d "$BODY" "$URL" > "/tmp/c.$i" ) &
    i=$((i+1))
  done
  wait
  i=1
  while [ "$i" -le "$N" ]; do
    printf "%s %s\n" "$(cat /tmp/c.$i 2>/dev/null)" "$(cat /tmp/b.$i 2>/dev/null)"
    i=$((i+1))
  done
' sh "$N" "$BODY2" "$BACKEND/api/incidents")

n201=0; n200=0; nbad=0; ids=""
while IFS= read -r ln; do
  [ -z "$ln" ] && continue
  code=${ln%% *}; body=${ln#* }
  case "$code" in
    201) n201=$((n201+1)); ids="$ids $(echo "$body" | jq -r '.id // empty')" ;;
    200) n200=$((n200+1)); ids="$ids $(echo "$body" | jq -r '.id // empty')" ;;
    *)   nbad=$((nbad+1)) ;;  # 5xx / SQLITE_BUSY(500) / UNIQUE(500) / curl 실패(000)
  esac
done <<< "$lines"

uniq_ids=$(echo $ids | tr ' ' '\n' | sort -u | grep -c . )
cnt=$(db_query "SELECT COUNT(*) FROM incidents WHERE alert_id='$AID2'")
echo "concurrent N=$N: 201=$n201 200=$n200 bad(5xx/BUSY/UNIQUE/fail)=$nbad distinct_ids=$uniq_ids db_count=$cnt"
echo "concurrent codes:"; echo "$lines" | awk '{print $1}' | sort | uniq -c

[ "$n201" = "1" ] && [ "$n200" = "$((N-1))" ] && [ "$nbad" = "0" ] \
  && [ "$uniq_ids" = "1" ] && [ "$cnt" = "1" ] \
  && ok "멱등 원자성 (순차 + N=$N 동시)" \
  || nok "동시성 멱등 위반 (201=$n201 200=$n200 bad=$nbad ids=$uniq_ids count=$cnt)"
