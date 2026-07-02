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

# protect 수행 흔적 (컨테이너 로그)
plog=$(docker logs "$REC_CONTAINER" 2>&1 | grep -c 'Protecting segments for' || true)
[ "$plog" -ge 1 ] && ok "로그: 'Protecting segments for' ${plog}건 (protect 경로 실행 흔적)" \
                  || nok "protect 실행 로그 없음"

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
[ "$head" = "OKALL" ] || FAILED=1

if [ "${ALLOW_MUTATING:-0}" != "1" ]; then
  if [ "$FAILED" -eq 0 ]; then
    echo "VERDICT F: OK (사후 관측 — protect 구간 세그먼트가 윈도우 2배+ 경과에도 잔존, protect 이후 도착분 포함. 202 응답코드·window=1 절차는 mutating으로 미실행)"
  else
    echo "VERDICT F: NOK (사후 관측 기준)"; exit 1
  fi
  exit 0
fi
echo "  [!!] MUTATING 절차: POST /api/archives/protect {incidentId, streamKeys:[k], incidentTime=now} → 202 확인 → 3×윈도우 대기 → 세그먼트 잔존 확인"
verdict F
