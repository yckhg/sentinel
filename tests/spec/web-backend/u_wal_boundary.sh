#!/usr/bin/env bash
# U. WAL 경계·회수 — spec: docs/spec/web-backend.md 단언 U
#   적대적 부하(다수 writer + 다수 상시 reader) 중·직후 /data/sentinel.db-wal 크기가
#   고정 상한 내 유지(무한성장 없음, reader-pin 하에서도)되고, 부하가 멎고 체크포인트
#   주기가 지나면 WAL 이 크게 축소(회수)된다.
#   - reader-pin 절 구동: writer(WR) 와 함께 상시 reader(RD 개, GET /api/incidents 루프)를
#     같이 돌려 "다수 유휴 reader 보유해도 경계 유지" 절을 실제로 구동.
#   - 정직 표집: 부하 컨테이너 내부 고빈도(150ms) 백그라운드 샘플러로 WAL 크기를 연속 기록
#     → peak 를 놓치지 않음. 표본 개수 n·min·peak·최종 출력.
#   - 회수 엄격: 부하 정지 + 체크포인트 주기 후 post <= RECLAIM(1MB) 엄격 단언(느슨한 OR 없음).
#   - 비공허: peak 가 초기 대비 충분히 크지 않으면(부하 미발생) SKIPPED(low-load) — 초록 위조 금지.
# SKIP: mutating — 수백 회 incidents POST + devices.seen(update) 부하.
# 격리: run-고유 siteId. devices 잔류 억제: worker 당 device id 1개(반복 update).
set -uo pipefail; . "$(dirname "$0")/../lib-web.sh"
require_mutating
T=$(get_token) || exit 1

# 고정 상한(구현 측정치). main.go: 250ms 크기표집 + 2MB 트리거 TRUNCATE + 1s 주기 +
# ConnMaxLifetime 15s(reader snapshot 해제). 적대적 부하에서 관측 상한에 여유 마진.
CAP=${U_CAP:-5242880}        # 5 MB 고정 상한 (peak > CAP → NOK)
RECLAIM=${U_RECLAIM:-1048576} # 부하 후 회수 상한 1 MB (post > RECLAIM → NOK)
NONVAC=${U_NONVAC:-524288}    # 비공허 최소 peak 성장 512KB (미만 → SKIP low-load)
TRIGGER=${U_TRIGGER:-2097152} # 2 MB size-triggered TRUNCATE 임계 (정보성: peak 가 이를 넘으면
                              #   크기 트리거 체크포인트가 실제 발화했음을 실증; 미도달은 NOK 아님)
WR=${U_WRITERS:-24}          # writer 워커 수 (>=16; 2MB 트리거 실증 위해 상향)
RD=${U_READERS:-24}          # 상시 reader 워커 수 (>=20)
WN=${U_WCOUNT:-150}          # writer 당 (incident+devices.seen) 라운드 수 (2MB 실증 위해 상향)
RN=${U_RCOUNT:-500}          # reader 당 GET 루프 수 (writer 전 구간 pin 유지)
IDLE=${U_IDLE:-18}           # 유휴 상시 reader 보유 시간(초) — ConnMaxLifetime(15s) 1회 recycle 초과
SID="spectdd-u-$(date +%s)-$$"

TAG="$SID"

# --- per-run cleanup (판정 후 EXIT 훅) : 이 실행이 만든 run-태그 행만 삭제 ----------------
#   db_query 헬퍼는 볼륨을 :ro 로 마운트해 삭제 불가 → 여기서 :rw 로 마운트해 DELETE.
#   ok/nok/skip 판정 이후 trap 으로 실행되어 verdict 에 영향 없음. best-effort(실패는 경고만).
#   site_id 로 스코프(SPEC-U-* deviceId 는 run 간 재사용되므로 device_id 아닌 site_id 로 격리).
cleanup_run() {
  local bi bd ai ad
  bi=$(db_query "SELECT COUNT(*) FROM incidents WHERE site_id='$SID'" 2>/dev/null)
  bd=$(db_query "SELECT COUNT(*) FROM devices  WHERE site_id='$SID'" 2>/dev/null)
  docker run --rm -v sentinel_db-data:/data "$PY_IMG" python -c '
import sqlite3, sys
con = sqlite3.connect("/data/sentinel.db", timeout=20)
con.execute("PRAGMA busy_timeout=20000")
for stmt in sys.argv[1:]:
    try: con.execute(stmt)
    except Exception as e: sys.stderr.write("cleanup DELETE warn: %s\n" % e)
con.commit(); con.close()
' "DELETE FROM incidents WHERE site_id='$SID'" \
  "DELETE FROM devices WHERE site_id='$SID'" 2>&1 | sed 's/^/  cleanup-warn: /' || true
  ai=$(db_query "SELECT COUNT(*) FROM incidents WHERE site_id='$SID'" 2>/dev/null)
  ad=$(db_query "SELECT COUNT(*) FROM devices  WHERE site_id='$SID'" 2>/dev/null)
  echo "  cleanup(site_id=$SID): incidents ${bi:-?}->${ai:-?}  devices ${bd:-?}->${ad:-?}"
}
trap cleanup_run EXIT
init=$(docker run --rm -v sentinel_db-data:/data:ro --entrypoint sh "$CURL_IMG" -c \
  'stat -c %s /data/sentinel.db-wal 2>/dev/null || echo 0')
echo "WAL init bytes=$init  (WR=$WR RD=$RD WN=$WN RN=$RN CAP=$CAP RECLAIM=$RECLAIM)"

# 부하+표집을 한 컨테이너에서: 볼륨 :ro 마운트(실 WAL stat) + 네트워크(curl).
# 내부 백그라운드 샘플러가 150ms 마다 WAL 크기를 samples 파일에 기록.
# 라인 프로토콜: "SAMPLE <bytes>" (표집), 그 외는 무시.
raw=$(docker run --rm --network "$NET" -v sentinel_db-data:/data:ro --entrypoint sh "$CURL_IMG" -c '
  UI="$1"; UD="$2"; WR="$3"; RD="$4"; WN="$5"; RN="$6"; TOK="$7"; TAG="$8"; GI="$9"; IDLE="${10}"
  # 고빈도 WAL 샘플러 (백그라운드)
  ( while :; do
      s=$(stat -c %s /data/sentinel.db-wal 2>/dev/null || echo 0)
      echo "SAMPLE $s"
      sleep 0.15
    done ) > /tmp/samples 2>/dev/null &
  SPID=$!
  # writer 워커: incident(고유 alertId) + devices.seen(worker당 device 1개, 반복 update)
  PIDS=""
  w=0
  while [ "$w" -lt "$WR" ]; do
    ( c=0
      while [ "$c" -lt "$WN" ]; do
        curl -s -o /dev/null -X POST -H "Content-Type: application/json" \
          -d "{\"siteId\":\"$TAG\",\"description\":\"u\",\"isTest\":true,\"alertId\":\"${TAG}-w${w}-${c}\"}" "$UI"
        curl -s -o /dev/null -X POST -H "Content-Type: application/json" \
          -d "{\"siteId\":\"$TAG\",\"deviceId\":\"SPEC-U-${w}\"}" "$UD"
        c=$((c+1))
      done ) &
    PIDS="$PIDS $!"
    w=$((w+1))
  done
  # 상시 reader 워커: GET /api/incidents 루프 (WAL snapshot pin 유발; churn 계열)
  r=0
  while [ "$r" -lt "$RD" ]; do
    ( k=0
      while [ "$k" -lt "$RN" ]; do
        curl -s -o /dev/null -H "Authorization: Bearer $TOK" "$GI?limit=100"
        k=$((k+1))
      done ) &
    PIDS="$PIDS $!"
    r=$((r+1))
  done
  # 유휴 상시 reader (churn 아님): 부하 창 전체 동안 저빈도 read + sleep 로 풀 커넥션을
  # 상주시켜 "다수 유휴 reader 상시 보유해도 경계 유지" 절을 실제 구동. IDLE(>=15s ConnMaxLifetime)
  # 동안 유지 → 최소 1회 recycle 를 가로질러 pin 하에서의 회수를 정직하게 시험한다.
  ( end=$(( $(date +%s) + IDLE ))
    while [ "$(date +%s)" -lt "$end" ]; do
      curl -s -o /dev/null -H "Authorization: Bearer $TOK" "$GI?limit=1"
      sleep 2
    done ) &
  PIDS="$PIDS $!"
  # writer+reader 워커만 대기 (샘플러 $SPID 는 무한 루프이므로 wait 대상에서 제외 —
  # 인자 없는 wait 는 샘플러까지 기다려 컨테이너가 영원히 종료되지 않는다).
  wait $PIDS
  sleep 0.5     # 부하 종료 직후 한두 표본 더
  kill "$SPID" 2>/dev/null
  cat /tmp/samples
' sh "$BACKEND/api/incidents" "$BACKEND/api/devices/seen" "$WR" "$RD" "$WN" "$RN" "$T" "$TAG" "$BACKEND/api/incidents" "$IDLE")

# 표본 파싱
samples=$(echo "$raw" | awk '/^SAMPLE /{print $2}')
n=$(echo "$samples" | grep -c . )
peak=0; minv=""
for s in $samples; do
  [ -z "$s" ] && continue
  [ "$s" -gt "$peak" ] && peak=$s
  { [ -z "$minv" ] || [ "$s" -lt "$minv" ]; } && minv=$s
done
final_during=$(echo "$samples" | tail -1)
echo "under-load samples: n=$n min=${minv:-0} peak=$peak final_during=${final_during:-0}"

# 부하 후 회수: 체크포인트 주기(1s) + 크기표집(250ms) 여유
sleep 6
post=$(docker run --rm -v sentinel_db-data:/data:ro --entrypoint sh "$CURL_IMG" -c \
  'stat -c %s /data/sentinel.db-wal 2>/dev/null || echo 0')
echo "WAL post-load bytes=$post  (init=$init peak=$peak cap=$CAP reclaim<=$RECLAIM)"

growth=$((peak - init))
echo "판정 근거: growth(peak-init)=$growth (nonvac>=$NONVAC)  bound(peak<=$CAP)  reclaim(post<=$RECLAIM)"

# 정보성: peak 가 2MB 크기 트리거를 넘겼는지(= size-triggered TRUNCATE 발화 실증) 보고.
# 넘기지 못해도 NOK 아님 — 이 호스트 부하가 트리거에 못 미친 것일 뿐(경계/회수 단언은 유효).
if [ "$peak" -ge "$TRIGGER" ]; then
  echo "INFO: peak=${peak}B >= 2MB 트리거(${TRIGGER}B) — 크기 트리거 TRUNCATE 체크포인트 실증됨"
else
  echo "INFO: peak=${peak}B < 2MB 트리거(${TRIGGER}B) — 이 호스트 부하로 크기 트리거 미도달(정보성, NOK 아님)"
fi

if [ "$n" -lt 3 ]; then
  skip "표본 부족 (n=$n) — 샘플러 미작동/부하 컨테이너 조기종료"
fi
if [ "$growth" -lt "$NONVAC" ]; then
  skip "vacuous low-load — WAL 성장 $growth < $NONVAC (부하가 WAL을 밀어올리지 못함; 경계 단언 무의미)"
fi
if [ "$peak" -gt "$CAP" ]; then
  nok "WAL 경계 초과 — peak=${peak}B > CAP=${CAP}B (적대적 부하에서 상한 위반)"
fi
if [ "$post" -gt "$RECLAIM" ]; then
  nok "WAL 회수 실패 — post=${post}B > RECLAIM=${RECLAIM}B (TRUNCATE 체크포인트 미회수)"
fi
ok "WAL 경계 유지(peak=${peak}B<=${CAP}B, non-vacuous growth=${growth}B) + 부하 후 회수(post=${post}B<=${RECLAIM}B)"
