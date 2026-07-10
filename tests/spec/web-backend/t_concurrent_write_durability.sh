#!/usr/bin/env bash
# T. 동시 쓰기 내구성 — spec: docs/spec/web-backend.md 단언 T
#   상이 alertId(각기 다름) POST /api/incidents 를 N(>=30) 동시 실행 →
#   전부 201, SQLITE_BUSY/5xx 0건, 유실 0.
#   WAL + busy_timeout 직렬화로 락 경합이 5xx로 새지 않음을 확인.
# SKIP: mutating — 실제 incident N건 생성.
# 격리: run-고유 siteId + alertId prefix. 유실 판정은 고유 tag 행 수(persisted)가 권위.
#       공유 라이브 DB에서 전역 COUNT delta 는 병렬 유입(hw-gateway 등)에 취약 → advisory.
set -uo pipefail; . "$(dirname "$0")/../lib-web.sh"
require_mutating

N=${T_CONCURRENCY:-30}
TAG="spectdd-t-$(date +%s)-$$"   # 이 실행에 고유한 siteId/alertId prefix (다른 writer와 격리)

# --- per-run cleanup (판정 후 EXIT 훅) : 이 실행이 만든 run-태그 행만 삭제 ----------------
#   db_query 는 볼륨을 :ro 로 마운트해 삭제 불가 → 여기서 :rw 로 마운트해 DELETE.
#   ok/nok 판정 이후 trap 으로 실행되어 verdict 에 영향 없음. best-effort(실패는 경고만).
cleanup_run() {
  local b a
  b=$(db_query "SELECT COUNT(*) FROM incidents WHERE site_id='$TAG'" 2>/dev/null)
  docker run --rm -v sentinel_db-data:/data "$PY_IMG" python -c '
import sqlite3, sys
con = sqlite3.connect("/data/sentinel.db", timeout=20)
con.execute("PRAGMA busy_timeout=20000")
for stmt in sys.argv[1:]:
    try: con.execute(stmt)
    except Exception as e: sys.stderr.write("cleanup DELETE warn: %s\n" % e)
con.commit(); con.close()
' "DELETE FROM incidents WHERE site_id='$TAG'" 2>&1 | sed 's/^/  cleanup-warn: /' || true
  a=$(db_query "SELECT COUNT(*) FROM incidents WHERE site_id='$TAG'" 2>/dev/null)
  echo "  cleanup(incidents site_id=$TAG): ${b:-?}->${a:-?}"
}
trap cleanup_run EXIT

before=$(db_query "SELECT COUNT(*) FROM incidents")

# 한 컨테이너에서 N개 curl 동시 발사. 각 요청은 고유 alertId(TAG-i) → dedup 미발화, 전부 신규.
lines=$(docker run --rm --network "$NET" --entrypoint sh "$CURL_IMG" -c '
  N="$1"; TAG="$2"; URL="$3"; i=1
  while [ "$i" -le "$N" ]; do
    BODY="{\"siteId\":\"$TAG\",\"description\":\"t\",\"isTest\":true,\"alertId\":\"${TAG}-$i\"}"
    ( curl -s -o /dev/null -w "%{http_code}\n" -X POST \
        -H "Content-Type: application/json" -d "$BODY" "$URL" > "/tmp/c.$i" ) &
    i=$((i+1))
  done
  wait
  i=1; while [ "$i" -le "$N" ]; do cat "/tmp/c.$i" 2>/dev/null; i=$((i+1)); done
' sh "$N" "$TAG" "$BACKEND/api/incidents")

n201=0; nbad=0
while IFS= read -r code; do
  [ -z "$code" ] && continue
  case "$code" in
    201) n201=$((n201+1)) ;;
    *)   nbad=$((nbad+1)) ;;  # 5xx / SQLITE_BUSY(500) / curl 실패(000) 등
  esac
done <<< "$lines"

after=$(db_query "SELECT COUNT(*) FROM incidents")
persisted=$(db_query "SELECT COUNT(*) FROM incidents WHERE alert_id LIKE '${TAG}-%'")
delta=$((after - before))
echo "N=$N 201=$n201 bad(5xx/BUSY/fail)=$nbad persisted(tag,authoritative)=$persisted global_delta(advisory)=$delta"
echo "codes:"; echo "$lines" | sort | uniq -c

# 권위 판정: 전부 201 + BUSY/5xx 0 + 고유 tag 행 정확히 N (유실 0). global_delta 는 advisory.
[ "$n201" = "$N" ] && [ "$nbad" = "0" ] && [ "$persisted" = "$N" ] \
  && ok "동시 쓰기 내구성 (N=$N 전부 201·유실0·BUSY0)" \
  || nok "동시 쓰기 결함 (201=$n201 bad=$nbad persisted=$persisted 기대=$N)"
