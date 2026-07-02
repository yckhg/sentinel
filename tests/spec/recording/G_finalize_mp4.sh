#!/usr/bin/env bash
# 단언 G. finalize가 MP4를 만든다 —
#   규정 절차: F 이후 POST /api/archives/finalize → 200, 60초 내 completed·sizeBytes>0,
#   {ARCHIVES_DIR}/{id}/k.mp4 존재 + ffprobe 유효 + download 200 video/mp4.
# SKIP(규정 절차): finalize POST는 MUTATING — 프로덕션 실행 금지.
# 사후 판정(READ-ONLY): 기존 protect→finalize 산출물(completed incident 아카이브)에 대해
#   메타 sizeBytes>0, mp4 파일 존재·크기 일치, ffprobe 판독, faststart(moov 선행),
#   GET /api/archives/{id}/download == 200 + video/mp4 를 검증.
. "$(dirname "$0")/common.sh"

tmp=$(mktemp -d)
rexec "wget -qO- $REC/api/archives" > "$tmp/archives.json"

# 가장 작은 completed incident 아카이브 선택 (다운로드 부하 최소화)
sel=$(python3 - "$tmp/archives.json" <<'EOF'
import json, sys
a = [x for x in json.load(open(sys.argv[1]))
     if x["status"] == "completed" and x["id"].startswith("incident_") and x["sizeBytes"] > 0]
if not a: sys.exit(1)
x = min(a, key=lambda x: x["sizeBytes"])
print(x["id"], x["streamKey"], x["sizeBytes"], x["filePath"])
EOF
) || { rm -rf "$tmp"; echo "VERDICT G: SKIPPED — completed incident 아카이브가 없어 사후 판정 불가"; exit 0; }
read -r aid key size fpath <<< "$sel"
info "표본: $aid (sizeBytes=$size)"

ok "메타: status=completed, sizeBytes=$size (>0)"

fsize=$(rexec "stat -c %s '$fpath' 2>/dev/null || wc -c < '$fpath'" | tr -d ' \r')
[ "$fsize" = "$size" ] && ok "파일 존재·크기 일치: $fpath ($fsize bytes)" || nok "파일 크기 불일치: meta=$size file=${fsize:-없음}"

probe=$(rexec "ffprobe -v error -show_entries format=format_name,duration -of csv=p=0 '$fpath'" | tr -d '\r')
case "$probe" in
  *mp4*) ok "ffprobe 유효 MP4: $probe";;
  *) nok "ffprobe 판독 실패/비MP4: $probe";;
esac

# faststart(메타데이터 선행 배치): 파일 앞 1MB 안에 moov 박스가 있는지
mo=$(rexec "dd if='$fpath' bs=1M count=1 2>/dev/null" | python3 -c "
import sys; d = sys.stdin.buffer.read()
m, d2 = d.find(b'moov'), d.find(b'mdat')
print('moov_first' if m >= 0 and (d2 < 0 or m < d2) else 'moov_not_first')")
[ "$mo" = "moov_first" ] && ok "moov 선행 배치(faststart) 확인" || nok "moov가 파일 선두 1MB 내 선행하지 않음 (스트리밍 재생 계약 위배 의심)"

http_head "$REC/api/archives/$aid/download"
[ "${STATUS:-}" = "200" ] && ok "download 200" || nok "download status=${STATUS:-none}"
case "${CTYPE:-}" in video/mp4*) ok "Content-Type: $CTYPE";; *) nok "Content-Type=$CTYPE (video/mp4 아님)";; esac

# finalize 실행 흔적 (protect→finalize 전이 로그)
fin=$(docker logs "$REC_CONTAINER" 2>&1 | grep -cE 'inaliz' || true)
info "로그 finalize 관련 라인 ${fin}건"

rm -rf "$tmp"
if [ "${ALLOW_MUTATING:-0}" != "1" ]; then
  if [ "$FAILED" -eq 0 ]; then
    echo "VERDICT G: OK (사후 관측 — 기존 finalize 산출물 검증. finalize 200 응답·60초 시한은 mutating으로 미실행)"
  else
    echo "VERDICT G: NOK (사후 관측 기준)"; exit 1
  fi
  exit 0
fi
echo "  [!!] MUTATING 절차: POST /api/archives/finalize {incidentId, resolvedAt=now} → 200 → 60초 내 completed 확인"
verdict G
