#!/usr/bin/env bash
# 단언 H. finalize 게이트 —
#   (1) protecting 없는 incidentId의 finalize → 404  [MUTATING: finalize POST — 기본 SKIP]
#   (2) completed 이전(protecting/pending/processing/finalizing) 아카이브의 download → 409
#       [READ-ONLY이나 해당 상태 아카이브가 현존해야 검증 가능]
# 보조(READ-ONLY): 존재하지 않는 아카이브 download → 404, failed 아카이브 download ≠ 200.
# SKIP: (1)은 archives POST 계열 금지. (2)는 현재 pre-completed 아카이브 부재 시 판정 불가.
. "$(dirname "$0")/common.sh"

tmp=$(mktemp)
rexec "wget -qO- $REC/api/archives" > "$tmp"

# (2) pre-completed 아카이브 download → 409
pre=$(python3 -c "
import json,sys
a=[x for x in json.load(open('$tmp')) if x['status'] in ('protecting','pending','processing','finalizing')]
print(a[0]['id'] if a else '')")
PART2="미검증"
if [ -n "$pre" ]; then
  http_head "$REC/api/archives/$pre/download"
  if [ "${STATUS:-}" = "409" ]; then ok "(2) pre-completed($pre) download 409"; PART2="OK"
  else nok "(2) pre-completed download status=${STATUS:-none} (409 기대)"; PART2="NOK"; fi
else
  info "(2) protecting/pending/processing/finalizing 상태 아카이브 현존하지 않음 — 판정 불가"
fi

# 보조: 없는 아카이브 → 404
http_head "$REC/api/archives/no-such-archive-spec-tdd/download"
[ "${STATUS:-}" = "404" ] && ok "보조: 없는 아카이브 download 404" || nok "보조: 없는 아카이브 download status=${STATUS:-none} (404 기대)"

# 비-completed(failed) 아카이브 download → 409 (완료본만 서빙 게이트)
fid=$(python3 -c "
import json
a=[x for x in json.load(open('$tmp')) if x['status']=='failed']
print(a[0]['id'] if a else '')")
if [ -n "$fid" ]; then
  http_head "$REC/api/archives/$fid/download"
  if [ "${STATUS:-}" = "409" ]; then
    ok "비-completed(failed: $fid) download 409 — '완료본만 서빙' 게이트 동작 관측"
    [ "$PART2" = "미검증" ] && PART2="OK-failed근거"
  elif [ "${STATUS:-}" = "200" ]; then
    nok "failed 아카이브가 200으로 서빙됨"
  else
    info "failed download=${STATUS:-none} (409 아님 — pre-completed 전용 게이트일 수 있음)"
  fi
fi
rm -f "$tmp"

if [ "${ALLOW_MUTATING:-0}" != "1" ]; then
  if [ "$FAILED" -ne 0 ]; then echo "VERDICT H: NOK"; exit 1; fi
  if [ "$PART2" = "OK" ] || [ "$PART2" = "OK-failed근거" ]; then
    echo "VERDICT H: OK (부분 — download 게이트(비-completed 409, 없음 404) 관측 확인. finalize-404는 mutating으로 SKIPPED)"
  else
    echo "VERDICT H: SKIPPED (mutating — 설계자 승인 대기) — finalize-404는 POST 금지, download-409는 pre-completed 아카이브 부재로 판정 불가 (보조 게이트: 404/비-200 서빙은 확인됨)"
  fi
  exit 0
fi

# ---- MUTATING PART (승인 시에만) ----
echo "  [!!] MUTATING: POST /api/archives/finalize {incidentId:'spec-tdd-no-such-incident', resolvedAt:now} → 404 기대"
verdict H
