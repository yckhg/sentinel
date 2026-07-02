#!/usr/bin/env bash
# 단언 D. 롤링 삭제 —
#   규정 절차: ROLLING_WINDOW_MINUTES=1 재기동 + 파일명 5분 전 더미 .ts 생성 → 90초 내 삭제.
# SKIP(규정 절차): 컨테이너 재기동·더미 파일 생성 모두 MUTATING — 프로덕션 실행 금지.
# 대신 사후 관측(READ-ONLY)으로 동등 판정: 현재 window(기본 60분) 기준
#   (i) age가 [50분, window+3분] 구간의 세그먼트가 존재 → "윈도우 이내 파일은 남는다"
#   (ii) age > window+3분 세그먼트는 전부 (컨테이너 기동 이후 생성된) 아카이브
#        보호구간 [from-15s, to+15s]에 속한다 → "미보호 초과분은 삭제된다"
. "$(dirname "$0")/common.sh"

WIN=$(rexec "printenv ROLLING_WINDOW_MINUTES" | tr -d '\r'); WIN=${WIN:-60}
STARTED=$(docker inspect "$REC_CONTAINER" --format '{{.State.StartedAt}}')
info "window=${WIN}min, container StartedAt=$STARTED"

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

def p(ts): return dt.datetime.strptime(ts, "%Y%m%d_%H%M%S")

segs = []  # (key, datetime)
for line in open(f"{tmp}/segs.txt"):
    line = line.strip()
    if not line.endswith(".ts"): continue
    key, name = line.split("/", 1)
    try: segs.append((key, p(name[:-3])))
    except ValueError: pass

arch = json.load(open(f"{tmp}/archives.json"))
slack = dt.timedelta(seconds=15)
prot = {}  # key -> [(from,to)]
for a in arch:
    created = dt.datetime.fromisoformat(a["createdAt"].replace("Z","+00:00")).replace(tzinfo=None)
    # 재시작 이전 생성분의 보호는 in-memory 소실 (protecting 상태는 주기 갱신으로 복원되나 현재 없음)
    if created < start and a.get("status") != "protecting":
        continue
    f = dt.datetime.fromisoformat(a["from"].replace("Z","+00:00")).replace(tzinfo=None)
    t = dt.datetime.fromisoformat(a["to"].replace("Z","+00:00")).replace(tzinfo=None)
    prot.setdefault(a["streamKey"], []).append((f - slack, t + slack))

wmin = dt.timedelta(minutes=win)
w_hi = dt.timedelta(minutes=win + 3)

near = [s for s in segs if wmin - dt.timedelta(minutes=10) <= now - s[1] <= w_hi]
stale = [(k, t) for k, t in segs if now - t > w_hi]
unprotected_stale = [
    (k, t) for k, t in stale
    if not any(f <= t <= e for f, e in prot.get(k, []))
]

lines = []
okall = True
if near:
    oldest = max(near, key=lambda s: now - s[1])
    age = (now - oldest[1]).total_seconds() / 60
    lines.append(f"ok (i) 윈도우 경계 부근 보존 확인: {oldest[0]}/{oldest[1]:%Y%m%d_%H%M%S}.ts age={age:.1f}min")
else:
    okall = False
    lines.append(f"NOK (i) age {win-10}~{win+3}분 구간 세그먼트가 전혀 없음 — 윈도우 이내 보존 확인 불가")
lines.append(f".. 전체 {len(segs)}개, 윈도우 초과(stale) {len(stale)}개, 그중 보호구간 밖 {len(unprotected_stale)}개")
if unprotected_stale:
    okall = False
    for k, t in unprotected_stale[:5]:
        lines.append(f"NOK (ii) 미보호 초과 세그먼트 잔존: {k}/{t:%Y%m%d_%H%M%S}.ts (age {(now-t).total_seconds()/60:.0f}min)")
else:
    lines.append(f"ok (ii) 윈도우 초과 잔존분 {len(stale)}개 전부 기동 후 아카이브 보호구간 내 — 미보호분은 삭제됨")
print(("OKALL" if okall else "NOKALL"))
print("\n".join(lines))
EOF
)
rm -rf "$tmp"
head=$(echo "$res" | head -1); echo "$res" | tail -n +2 | sed 's/^/  /'
[ "$head" = "OKALL" ] || FAILED=1

if [ "${ALLOW_MUTATING:-0}" != "1" ]; then
  if [ "$FAILED" -eq 0 ]; then
    echo "VERDICT D: OK (사후 관측 — 규정 절차(window=1 재기동+더미파일)는 mutating으로 미실행)"
  else
    echo "VERDICT D: NOK (사후 관측 기준)"; exit 1
  fi
  exit 0
fi

echo "  [!!] MUTATING 절차: ROLLING_WINDOW_MINUTES=1 재기동 → 파일명 5분 전 더미 .ts(1바이트) 생성 → 90초 내 삭제 확인"
verdict D
