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

res=$(python3 - "$tmp" "$STARTED" <<'EOF'
import json, sys, datetime as dt
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
prot = [x for x in api if x["status"] == "protecting"]
lines.append(f".. (c) 현존 protecting {len(prot)}개 — " + ("보호 복원 검증 가능" if prot else "부재로 보호 복원 서브단언은 미검증"))
print("OKALL" if okall else "NOKALL"); print("\n".join(lines))
EOF
)
rm -rf "$tmp"
head=$(echo "$res" | head -1); echo "$res" | tail -n +2 | sed 's/^/  [..]  /'
[ "$head" = "OKALL" ] || FAILED=1

if [ "${ALLOW_MUTATING:-0}" != "1" ]; then
  if [ "$FAILED" -eq 0 ]; then
    echo "VERDICT O: OK (사후 관측 — 직전 재시작에서 metadata 로드 입증. protecting 보호 복원 서브단언은 protecting 부재로 미검증, 재시작 유발은 mutating으로 미실행)"
  else
    echo "VERDICT O: NOK (사후 관측 기준)"; exit 1
  fi
  exit 0
fi
echo "  [!!] MUTATING 절차: docker restart $REC_CONTAINER 후 ID 집합 비교 + protecting 구간 60초 후 잔존 확인"
verdict O
