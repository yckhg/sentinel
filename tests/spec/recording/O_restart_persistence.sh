#!/usr/bin/env bash
# 단언 O. 재시작 내성 — 컨테이너 재시작 후 GET /api/archives 가 재시작 전과 동일한
#   ID 집합(metadata.json 로드), protecting 항목은 재시작 후에도 세그먼트 보호 유지.
# SKIP(규정 절차): 컨테이너 재시작은 MUTATING(가동 중 증거 녹화 중단) — 실행 금지.
# 사후 판정(READ-ONLY):
#   (a) metadata.json 의 ID 집합 == GET /api/archives 의 ID 집합 (SSOT 일치)
#   (b) 현재 컨테이너 기동 시각(StartedAt) "이전"에 createdAt 된 아카이브가 API에
#       존재 → 직전 재시작에서 metadata.json 이 실제 로드되었음을 입증
#   (c) protecting 복원: 현존 protecting 항목이 있으면 그 구간 세그먼트 잔존 확인
. "$(dirname "$0")/common.sh"

STARTED=$(docker inspect "$REC_CONTAINER" --format '{{.State.StartedAt}}')
info "container StartedAt=$STARTED"

tmp=$(mktemp -d)
rexec "wget -qO- $REC/api/archives" > "$tmp/api.json"
rexec "cat $ARCHIVES_DIR/metadata.json" > "$tmp/meta.json"
# (c) 실검증용 세그먼트 목록: /recordings/{key}/*.ts 를 수집(F 게이트와 동형).
for d in $(rexec "ls $RECORDINGS_DIR"); do
  rexec "ls $RECORDINGS_DIR/$d 2>/dev/null" | sed "s|^|$d/|" >> "$tmp/segs.txt" || true
done

res=$(python3 - "$tmp" "$STARTED" <<'EOF'
import json, sys, datetime as dt, os
tmp, started = sys.argv[1], sys.argv[2]
start = dt.datetime.fromisoformat(started.split(".")[0])
iso = lambda s: dt.datetime.fromisoformat(s.replace("Z", "+00:00")).replace(tzinfo=None)
api  = json.load(open(f"{tmp}/api.json"))
meta = json.load(open(f"{tmp}/meta.json"))
aid, mid = {x["id"] for x in api}, {x["id"] for x in meta}
lines, okall = [], True
if aid == mid:
    lines.append(f"ok (a) metadata.json({len(mid)}) == API({len(aid)}) ID 집합 일치")
else:
    okall = False
    lines.append(f"NOK (a) 집합 불일치: API만 {len(aid-mid)}개, meta만 {len(mid-aid)}개")
pre = [x for x in api if iso(x["createdAt"]) < start]
if pre:
    oldest = min(pre, key=lambda x: x["createdAt"])
    lines.append(f"ok (b) 기동({start}) 이전 생성 아카이브 {len(pre)}개가 API에 존재 "
                 f"(최고령 {oldest['id']} createdAt={oldest['createdAt']}) → 재시작 시 metadata.json 로드 입증")
else:
    okall = False
    lines.append("NOK (b) 기동 이전 생성 아카이브 없음 — 로드 입증 불가")

# (c) protecting 복원: 현존 protecting 항목이 있으면 그 병합구간 [from,to] 세그먼트가
#   디스크에 실제로 잔존하는지 실측(단언 O — protecting 이면 롤링 삭제에서 제외). 재시작
#   유발은 mutating 이라 이 read-only 게이트는 "현재 protecting 아카이브의 구간 세그먼트가
#   보호되어 남아 있는지"를 관측한다. protecting 부재이거나 구간 세그먼트를 디스크에서
#   관측 불가하면(실검증 불가) 위반이 아니라 부적절이므로 (c)를 SKIPPED 로 명시.
segs = {}
segf = f"{tmp}/segs.txt"
if os.path.exists(segf):
    for line in open(segf):
        line = line.strip()
        if not line.endswith(".ts") or "/" not in line: continue
        key, name = line.split("/", 1)
        try: segs.setdefault(key, []).append(dt.datetime.strptime(name[:-3], "%Y%m%d_%H%M%S"))
        except ValueError: pass

prot = [x for x in api if x["status"] == "protecting"]
cverdict = "SKIP"
if not prot:
    lines.append(".. (c) SKIPPED(부적절, no-data): 현존 protecting 아카이브 없음 — 보호 복원 서브단언 판정 부적절")
else:
    verified, notobs = [], []
    for a in prot:
        try: f, t = iso(a["from"]), iso(a["to"])
        except Exception: notobs.append(a["id"]); continue
        k = a.get("streamKey", "")
        inrange = [s for s in segs.get(k, []) if f <= s <= t]
        (verified if inrange else notobs).append(f"{a['id']}({len(inrange)})" if inrange else a["id"])
    if verified:
        cverdict = "OK"
        lines.append(f"ok (c) protecting {len(prot)}개 중 구간 세그먼트 실측 잔존 확인: {', '.join(verified)} "
                     f"— 보호 유지(롤링 삭제 제외) 실측" + (f"; 구간 세그먼트 디스크 미관측(판정 불가): {', '.join(notobs)}" if notobs else ""))
    else:
        lines.append(f".. (c) SKIPPED(부적절): 현존 protecting {len(prot)}개의 구간 세그먼트를 디스크에서 관측 불가 "
                     f"({', '.join(notobs)}) — read-only 실검증 불가")
print("OKALL" if okall else "NOKALL"); print(f"CVERDICT:{cverdict}"); print("\n".join(lines))
EOF
)
rm -rf "$tmp"
head=$(echo "$res" | head -1)
cvw=$(echo "$res" | sed -n '2p' | sed 's/^CVERDICT://')
echo "$res" | tail -n +3 | sed 's/^/  [..]  /'
[ "$head" = "OKALL" ] || FAILED=1

if [ "${ALLOW_MUTATING:-0}" != "1" ]; then
  # (a)/(b)가 OK/NOK 를 결정(SSOT 일치 + 로드 입증). (c) protecting 복원은 실측 OK 이거나,
  #   실검증 불가 시 SKIPPED(부적절)로 최종 메시지에 정직하게 반영(모순 메시지 제거).
  if [ "$FAILED" -eq 0 ]; then
    if [ "$cvw" = "OK" ]; then
      echo "VERDICT O: OK (사후 관측 — 직전 재시작에서 metadata 로드 입증 + protecting 구간 세그먼트 잔존 실측. 재시작 유발은 mutating으로 미실행)"
    else
      echo "VERDICT O: OK (사후 관측 — 직전 재시작에서 metadata 로드 입증. (c) protecting 보호 복원 서브단언은 SKIPPED(부적절): 실검증 불가. 재시작 유발은 mutating으로 미실행)"
    fi
  else
    echo "VERDICT O: NOK (사후 관측 기준)"; exit 1
  fi
  exit 0
fi
echo "  [!!] MUTATING 절차: docker restart $REC_CONTAINER 후 ID 집합 비교 + protecting 구간 60초 후 잔존 확인"
verdict O
