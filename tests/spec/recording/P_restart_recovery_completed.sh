#!/usr/bin/env bash
# 단언 P (미완료 아카이브 복구 — 세그먼트 존재 → completed 수렴)
#   docs/spec/recording.md §단언 P / §핵심 로직 7(기동 순서·보호 우선 재확립).
#
# MUTATING: 비종단 아카이브 seed(protect + metadata 상태 조작) + 컨테이너 재시작.
#   ALLOW_MUTATING=1 없이는 SKIP(exit 2) — 프로덕션에서 실행 금지.
#
# SKIP 근거(seed 전제 문서화): 정본 절차는 ROLLING_WINDOW_MINUTES=1 환경을 요구하나,
#   배포 컨테이너의 env 재구성(recreate)은 파괴적이므로 배포 window 를 그대로 두고
#   활성 녹화 키에 대해 protect 로 실세그먼트를 확보한 아카이브를 만든 뒤 그 상태를
#   processing(비종단·비-protecting)으로 조작하고 plain `docker restart` 로 판정한다.
#   (a) 로그 마커 순서(Recovery protection re-established → Rolling cleanup started)는
#   seed·window 와 무관하게 성립해야 하는 순서 불변식이므로 plain restart 만으로도 관측된다.
#
# 판정: (a) 로그에서 'Recovery protection re-established' 가 'Rolling cleanup started' 보다 먼저
#       (b) seed 아카이브가 60초 이내 completed(sizeBytes>0 + MP4 존재)로 수렴 (재개 성공)
#       (c) 병합 구간 [from,to) 원본 .ts 가 재시작 후에도 잔존(보호 재확립으로 롤링 삭제 방지)
. "$(dirname "$0")/common.sh"

req_mutating() {
  [ "${ALLOW_MUTATING:-0}" = "1" ] || {
    echo "SKIPPED P: (mutating — 설계자 승인 대기) 비종단 아카이브 seed + 재시작 필요 — ALLOW_MUTATING=1 로만 실행"
    exit 2
  }
}
req_mutating

REC_HEALTH="${REC}/healthz"
SID="spec-recP-$$"
NOW=$(date -u +%Y-%m-%dT%H:%M:%SZ)

# 활성 녹화 키(최근 실세그먼트 존재) — 유효 입력 재개 판정에는 실제 .ts 가 필요
K=$(active_key)
[ -n "$K" ] || { echo "SKIPPED P: 활성 recording 스트림 없음 — 유효 세그먼트 seed 불가"; exit 2; }
info "seed 대상 streamKey=$K incidentId=$SID (deployed window=$(rexec "printenv ROLLING_WINDOW_MINUTES" | tr -d '\r')m)"

tmp=$(mktemp -d)
cleanup() {
  # seed 잔여 정리(best-effort): incident 단위 DELETE (busybox wget 은 DELETE 미지원 → curl 컨테이너)
  docker run --rm --network "${NET:-sentinel_sentinel-net}" curlimages/curl:latest \
    -s -o /dev/null -X DELETE "http://recording:8080/api/archives/incident/$SID" >/dev/null 2>&1 || true
  rm -rf "$tmp"
}
trap cleanup EXIT

# 1) protect 로 실세그먼트를 보유한 아카이브 생성 (from=incidentTime-1h, to≈now)
POST=$(rexec "wget -qO- --header='Content-Type: application/json' --post-data='{\"incidentId\":\"$SID\",\"streamKeys\":[\"$K\"],\"incidentTime\":\"$NOW\"}' $REC/api/archives/protect")
info "protect 응답: $POST"

rexec "wget -qO- $REC/api/archives" > "$tmp/before.json"
AID=$(python3 -c 'import json,sys
a=[x for x in json.load(open(sys.argv[1])) if x["incidentId"]==sys.argv[2]]
print(a[0]["id"] if a else "")' "$tmp/before.json" "$SID")
[ -n "$AID" ] || { nok "seed 아카이브 생성 실패(protect 응답 확인)"; echo "VERDICT P: NOK"; exit 1; }
info "seed archiveId=$AID"

# 2) 상태를 processing(비종단·비-protecting)으로 조작 → 기동 복구 대상화
docker cp "$REC_CONTAINER":"$ARCHIVES_DIR/metadata.json" "$tmp/meta.json"
python3 -c 'import json,sys
p,aid=sys.argv[1],sys.argv[2]
d=json.load(open(p))
for x in d:
    if x["id"]==aid:
        x["status"]="processing"; x["sizeBytes"]=0; x["filePath"]=""; x.pop("error",None)
json.dump(d,open(p,"w"),indent=2)' "$tmp/meta.json" "$AID"
docker cp "$tmp/meta.json" "$REC_CONTAINER":"$ARCHIVES_DIR/metadata.json"

# 병합 구간 [from,to) 원본 세그먼트 목록(재시작 전)
rexec "ls $RECORDINGS_DIR/$K 2>/dev/null" > "$tmp/segs_before.txt" || true

# 3) plain 재시작 (env 불변)
info "docker restart $REC_CONTAINER ..."
docker restart "$REC_CONTAINER" >/dev/null

# 기동 대기(healthz 200)
for _ in $(seq 1 30); do
  http_get "$REC_HEALTH" >/dev/null 2>&1
  [ "${STATUS:-}" = "200" ] && break
  sleep 1
done
[ "${STATUS:-}" = "200" ] && info "recording 재기동 완료" || nok "재기동 후 healthz 200 미도달(STATUS=${STATUS:-none})"

# (a) 로그 마커 순서 — 최근 부팅 슬라이스 내에서 판정
LOGS=$(docker logs "$REC_CONTAINER" 2>&1)
BOOT=$(printf '%s\n' "$LOGS" | grep -n 'Recording service starting' | tail -1 | cut -d: -f1)
SLICE=$(printf '%s\n' "$LOGS" | tail -n +"${BOOT:-1}")
RE=$(printf '%s\n' "$SLICE" | grep -n 'Recovery protection re-established' | head -1 | cut -d: -f1)
RC=$(printf '%s\n' "$SLICE" | grep -n 'Rolling cleanup started' | head -1 | cut -d: -f1)
if [ -n "$RE" ] && [ -n "$RC" ] && [ "$RE" -lt "$RC" ]; then
  ok "(a) 'Recovery protection re-established'(#$RE) 가 'Rolling cleanup started'(#$RC) 보다 먼저 — 보호 우선 순서 계약"
else
  nok "(a) 순서 계약 위배/마커 부재: RE=${RE:-none} RC=${RC:-none}"
fi

# (b) 60초 이내 completed 수렴
STATUS_A=""; SIZE_A=0; FP_A=""
end=$((SECONDS+65))
while [ "$SECONDS" -lt "$end" ]; do
  read -r STATUS_A SIZE_A FP_A <<EOF
$(rexec "wget -qO- $REC/api/archives" | python3 -c 'import json,sys
a=[x for x in json.load(sys.stdin) if x["id"]==sys.argv[1]]
print(a[0]["status"], a[0].get("sizeBytes",0), a[0].get("filePath","-")) if a else print("MISSING 0 -")' "$AID")
EOF
  { [ "$STATUS_A" = "completed" ] && [ "${SIZE_A:-0}" -gt 0 ]; } && break
  [ "$STATUS_A" = "failed" ] && break
  sleep 3
done
if [ "$STATUS_A" = "completed" ] && [ "${SIZE_A:-0}" -gt 0 ]; then
  ok "(b) 60초 이내 completed 수렴: sizeBytes=$SIZE_A"
  MP4=$(rexec "stat -c %s '$FP_A' 2>/dev/null || wc -c < '$FP_A'" | tr -d ' \r')
  [ -n "$MP4" ] && [ "${MP4:-0}" -gt 0 ] && ok "(b) MP4 존재: $FP_A ($MP4 bytes)" || nok "(b) MP4 미존재/0바이트: $FP_A"
else
  nok "(b) completed 미수렴: status=$STATUS_A sizeBytes=${SIZE_A:-0} (유효 세그먼트 재개는 completed 로만 수렴해야 함)"
fi

# (c) 병합 구간 세그먼트 잔존 (보호 재확립)
rexec "ls $RECORDINGS_DIR/$K 2>/dev/null" > "$tmp/segs_after.txt" || true
MISS=$(python3 -c 'import json,sys,datetime as dt
tmp,aid=sys.argv[1],sys.argv[2]
d=json.load(open(f"{tmp}/before.json"))
a=[x for x in d if x["id"]==aid][0]
iso=lambda s: dt.datetime.fromisoformat(s.replace("Z","+00:00")).replace(tzinfo=None)
f,t=iso(a["from"]),iso(a["to"])
def parse(n):
    try: return dt.datetime.strptime(n[:-3],"%Y%m%d_%H%M%S")
    except Exception: return None
def inrange(fn):
    p=parse(fn); return p is not None and f<=p<=t
before={l.strip() for l in open(f"{tmp}/segs_before.txt") if l.strip().endswith(".ts") and inrange(l.strip())}
after={l.strip() for l in open(f"{tmp}/segs_after.txt") if l.strip().endswith(".ts")}
miss=sorted(before-after)
print(len(before)); print(",".join(miss[:8]))' "$tmp" "$AID")
NBEFORE=$(printf '%s\n' "$MISS" | head -1)
MISSLIST=$(printf '%s\n' "$MISS" | tail -1)
if [ -z "$MISSLIST" ]; then
  ok "(c) 병합 구간 세그먼트 ${NBEFORE}개 전부 재시작 후 잔존 — 보호 재확립"
else
  nok "(c) 병합 구간 세그먼트 소실(보호 재확립 실패): $MISSLIST"
fi

if [ "$FAILED" -eq 0 ]; then echo "VERDICT P: OK"; exit 0; else echo "VERDICT P: NOK"; exit 1; fi
