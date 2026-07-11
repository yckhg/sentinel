#!/usr/bin/env bash
# 단언 P-2 (미완료 아카이브 복구 — 세그먼트 소실 → failed 종단 강제)
#   docs/spec/recording.md §단언 P-2 / §핵심 로직 7.
#
# MUTATING: metadata.json 에 비종단 아카이브 항목 seed(주입) + 컨테이너 재시작.
#   ALLOW_MUTATING=1 없이는 SKIP(exit 2) — 프로덕션에서 실행 금지.
#
# 비파괴 seed: 실운영 footage 를 지우지 않는다. 존재하지 않는 합성 streamKey(dir 부재)에 대해
#   3시간 전 구간 [from,to) 를 갖는 processing 아카이브를 metadata.json 에 주입한다 →
#   그 구간의 원본 .ts 는 애초에 존재하지 않으므로 "세그먼트 소실" 조건을 비파괴로 재현한다.
#
# 판정: 재시작 60초 이내에 seed 아카이브가 failed 로 종단 전이하고 사유(error/lastError)가
#       비어있지 않으며, 절대 pending/processing/finalizing 에 무기한 고착되지 않는다.
#       (세그먼트 없는데 completed 가 되면 NOK — 불가능한 성공 표기)
. "$(dirname "$0")/common.sh"

req_mutating() {
  [ "${ALLOW_MUTATING:-0}" = "1" ] || {
    echo "SKIPPED P-2: (mutating — 설계자 승인 대기) 비종단 아카이브 seed(주입) + 재시작 필요 — ALLOW_MUTATING=1 로만 실행"
    exit 2
  }
}
req_mutating

REC_HEALTH="${REC}/healthz"
SID="spec-recP2-$$"
SKEY="spec-recP2-nokey-$$"     # 존재하지 않는 스트림 키(세그먼트 디렉터리 부재)
FROM=$(date -u -d '-3 hours' +%Y-%m-%dT%H:%M:%SZ 2>/dev/null || date -u -v-3H +%Y-%m-%dT%H:%M:%SZ)
TO=$(date -u -d '-2 hours 55 minutes' +%Y-%m-%dT%H:%M:%SZ 2>/dev/null || date -u -v-2H -v-55M +%Y-%m-%dT%H:%M:%SZ)
NOW=$(date -u +%Y-%m-%dT%H:%M:%SZ)
AID="${SID}_${SKEY}_$(date -u -d '-3 hours' +%Y%m%d_%H%M%S 2>/dev/null || date -u -v-3H +%Y%m%d_%H%M%S)"

tmp=$(mktemp -d)
cleanup() {
  docker run --rm --network "${NET:-sentinel_sentinel-net}" curlimages/curl:latest \
    -s -o /dev/null -X DELETE "http://recording:8080/api/archives/incident/$SID" >/dev/null 2>&1 || true
  rm -rf "$tmp"
}
trap cleanup EXIT

info "seed(주입) archiveId=$AID streamKey=$SKEY from=$FROM to=$TO (합성·비파괴)"

# 1) metadata.json 에 비종단(processing) 항목 주입 — 세그먼트는 애초에 부재
docker cp "$REC_CONTAINER":"$ARCHIVES_DIR/metadata.json" "$tmp/meta.json"
python3 -c 'import json,sys
p,aid,sid,skey,fr,to,now=sys.argv[1:8]
d=json.load(open(p))
if any(x["id"]==aid for x in d):
    pass
else:
    d.append({"id":aid,"incidentId":sid,"streamKey":skey,"from":fr,"to":to,
              "createdAt":now,"sizeBytes":0,"filePath":"","status":"processing",
              "incidentTime":fr})
json.dump(d,open(p,"w"),indent=2)' "$tmp/meta.json" "$AID" "$SID" "$SKEY" "$FROM" "$TO" "$NOW"
docker cp "$tmp/meta.json" "$REC_CONTAINER":"$ARCHIVES_DIR/metadata.json"

# 2) 재시작
info "docker restart $REC_CONTAINER ..."
docker restart "$REC_CONTAINER" >/dev/null
for _ in $(seq 1 30); do
  http_get "$REC_HEALTH" >/dev/null 2>&1
  [ "${STATUS:-}" = "200" ] && break
  sleep 1
done
[ "${STATUS:-}" = "200" ] && info "recording 재기동 완료" || nok "재기동 후 healthz 200 미도달(STATUS=${STATUS:-none})"

# 3) 60초 이내 failed(+사유) 종단 판정
ST=""; ERR=""
end=$((SECONDS+65))
while [ "$SECONDS" -lt "$end" ]; do
  read -r ST ERR <<EOF
$(rexec "wget -qO- $REC/api/archives" | python3 -c 'import json,sys
a=[x for x in json.load(sys.stdin) if x["id"]==sys.argv[1]]
if not a: print("MISSING", ""); raise SystemExit
x=a[0]
reason=(x.get("error") or x.get("lastError") or "").replace(" ","_") or "-"
print(x["status"], reason)' "$AID")
EOF
  [ "$ST" = "failed" ] && break
  [ "$ST" = "completed" ] && break
  sleep 3
done

case "$ST" in
  failed)
    ok "60초 이내 failed 종단 전이"
    [ -n "$ERR" ] && [ "$ERR" != "-" ] && ok "사유(error/lastError) 비어있지 않음: $ERR" \
                                       || nok "failed 이나 사유가 비어있음 (P-2 는 사유 필수)"
    ;;
  completed)
    nok "세그먼트 부재인데 completed — 불가능한 성공 표기 (P-2 위배)"
    ;;
  processing|pending|finalizing)
    nok "60초 초과 비종단 고착: status=$ST (재시작을 넘겨 비종단 고착 금지 — 반드시 failed+사유)"
    ;;
  *)
    nok "판정 불가: status='${ST:-none}' (seed 아카이브 미발견?)"
    ;;
esac

if [ "$FAILED" -eq 0 ]; then echo "VERDICT P-2: OK"; exit 0; else echo "VERDICT P-2: NOK"; exit 1; fi
