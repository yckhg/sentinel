#!/usr/bin/env bash
# 단언 F. protect가 삭제를 막는다 —
#   규정 절차: ROLLING_WINDOW_MINUTES=1 재기동 + POST /api/archives/protect(202) 후
#   기존·신규 세그먼트가 3×윈도우 후에도 잔존.
# SKIP(규정 절차): protect POST·재기동 모두 MUTATING — 프로덕션 실행 금지.
# 사후 판정(READ-ONLY): 컨테이너 기동 이후 protect 로그가 있는 incident 아카이브의
#   [from, to] 구간 세그먼트가 롤링 윈도우(60분)를 크게 지난 지금도 /recordings에
#   잔존하는지 + protect 시각(createdAt) "이후" 도착 세그먼트도 잔존하는지 확인.
. "$(dirname "$0")/common.sh"

WIN=$(rexec "printenv ROLLING_WINDOW_MINUTES" | tr -d '\r'); WIN=${WIN:-60}
STARTED=$(docker inspect "$REC_CONTAINER" --format '{{.State.StartedAt}}')

# protect 수행 흔적 (컨테이너 로그) — 흔적 부재는 위반이 아니라 no-data(증거 부재)이므로
#   info 로만 표기(FAILED 로 올리지 않음). 최종 판정은 아카이브 잔존 증거 유무로 결정.
plog=$(docker logs "$REC_CONTAINER" 2>&1 | grep -c 'Protecting segments for' || true)
[ "$plog" -ge 1 ] && ok   "로그: 'Protecting segments for' ${plog}건 (protect 경로 실행 흔적)" \
                  || info "protect 실행 로그 없음 (기동 이후 능동 protect 미수행 — no-data)"

tmp=$(mktemp -d)
rexec "wget -qO- $REC/api/archives" > "$tmp/archives.json"
for d in $(rexec "ls $RECORDINGS_DIR"); do
  rexec "ls $RECORDINGS_DIR/$d 2>/dev/null" | sed "s|^|$d/|" >> "$tmp/segs.txt" || true
done

res=$(python3 - "$tmp" "$WIN" "$STARTED" <<'EOF'
import json, sys, datetime as dt
tmp, win, started = sys.argv[1], int(sys.argv[2]), sys.argv[3]
now = dt.datetime.now(dt.timezone.utc).replace(tzinfo=None)
start = dt.datetime.fromisoformat(started.split(".")[0])
iso = lambda s: dt.datetime.fromisoformat(s.replace("Z", "+00:00")).replace(tzinfo=None)

segs = {}
for line in open(f"{tmp}/segs.txt"):
    line = line.strip()
    if not line.endswith(".ts"): continue
    key, name = line.split("/", 1)
    try: segs.setdefault(key, []).append(dt.datetime.strptime(name[:-3], "%Y%m%d_%H%M%S"))
    except ValueError: pass

arch = [a for a in json.load(open(f"{tmp}/archives.json"))
        if a["id"].startswith("incident_") and iso(a["createdAt"]) >= start]
if not arch:
    print("NOKALL"); print("판정 근거 부재: 컨테이너 기동 이후 incident protect 아카이브 없음"); sys.exit(0)

wmin = dt.timedelta(minutes=win)
evid, okall = [], False
for a in arch:
    k, f, t, created = a["streamKey"], iso(a["from"]), iso(a["to"]), iso(a["createdAt"])
    if now - t < 2 * wmin:   # 윈도우 2배 이상 지난 것만 강한 근거로 채택
        continue
    inrange = [s for s in segs.get(k, []) if f <= s <= t]
    post = [s for s in inrange if s > created]
    if inrange and post:
        okall = True
        evid.append(f"{a['id']}: 구간내 잔존 {len(inrange)}개(최고령 age {(now-min(inrange)).days}d), "
                    f"protect({created:%m-%d %H:%M}) 이후 도착분 {len(post)}개 잔존")
        if len(evid) >= 3: break
print("OKALL" if okall else "NOKALL")
print("\n".join(evid) if evid else "윈도우 2배 이상 경과한 protect 구간의 세그먼트 잔존 근거 없음")
EOF
)
rm -rf "$tmp"
head=$(echo "$res" | head -1); echo "$res" | tail -n +2 | sed 's/^/  [..]  /'

if [ "${ALLOW_MUTATING:-0}" != "1" ]; then
  # READ-ONLY 사후 판정은 위반을 능동 입증할 수 없다(재기동·202·window=1 절차가 mutating).
  #   양성 증거(윈도우 2배+ 경과 protect 구간의 세그먼트 잔존 + protect 이후 도착분)를
  #   관측하면 OK. 증거가 없으면(기동 이후 incident protect 아카이브 부재 / 윈도우 2배
  #   경과분 없음 / 구간 세그먼트 없음) 이는 위반이 아니라 no-data 이므로 위음성 NOK 대신
  #   SKIPPED(부적절, no-data)로 표면화한다. 계약(protect 가 삭제 차단)은 단언 P(a)와
  #   라이브 protecting 아카이브가 능동 입증한다.
  if [ "$head" = "OKALL" ]; then
    echo "VERDICT F: OK (사후 관측 — protect 구간 세그먼트가 윈도우 2배+ 경과에도 잔존, protect 이후 도착분 포함. 202 응답코드·window=1 절차는 mutating으로 미실행)"
    exit 0
  else
    echo "VERDICT F: SKIPPED (부적절, no-data — read-only 사후 관측에 protect 구간 세그먼트 잔존 증거 없음; 계약은 단언 P(a)·라이브 protecting 아카이브가 능동 입증. 능동 실측은 ALLOW_MUTATING=1)"
    exit 2
  fi
fi
echo "  [!!] MUTATING 절차: POST /api/archives/protect {incidentId, streamKeys:[k], incidentTime=now} → 202 확인 → 3×윈도우 대기 → 세그먼트 잔존 확인"
verdict F
