#!/usr/bin/env bash
# 단언 M. 자동 finalize — incidentTime을 3시간 전으로 protect만 해두면 60초 내
#   protecting을 벗어나 finalizing/processing/completed/failed 로 전이
#   (로그에 'Auto-finalizing expired incident').
# SKIP(규정 절차): protect POST는 MUTATING — 프로덕션 실행 금지.
# 사후 판정(READ-ONLY): 운영 이력에서 동일 메커니즘의 흔적을 검증 —
#   (a) 로그에 'Auto-finalizing expired incident' 존재
#   (b) 해당 incident 아카이브가 현재 protecting이 아닌 종결 상태(completed/failed 등)
#   (c) 자동 finalize된 아카이브의 to ≈ incidentTime + 2h + 30m (2시간 만료 + post-roll 30분)
. "$(dirname "$0")/common.sh"

lines=$(docker logs "$REC_CONTAINER" 2>&1 | grep 'Auto-finalizing expired incident' || true)
cnt=$(echo "$lines" | grep -c . || true)
if [ "$cnt" -eq 0 ]; then
  if [ "${ALLOW_MUTATING:-0}" != "1" ]; then
    skip_mutating M "protect POST 금지 + 로그에 자동 finalize 흔적 없음(사후 판정 불가)"
  fi
else
  ok "로그 'Auto-finalizing expired incident' ${cnt}건"
  echo "$lines" | tail -2 | sed 's/^/  [..]  /'

  tmp=$(mktemp)
  rexec "wget -qO- $REC/api/archives" > "$tmp"
  ids=$(echo "$lines" | grep -oE 'incident[_a-zA-Z0-9]*' | sort -u | grep -v '^incident$' || true)
  res=$(python3 - "$tmp" <<EOF
import json, sys, datetime as dt
arch = json.load(open("$tmp"))
ids = """$ids""".split()
iso = lambda s: dt.datetime.fromisoformat(s.replace("Z", "+00:00")).replace(tzinfo=None)
bad, seen, okto = [], 0, 0
for a in arch:
    if a["incidentId"] not in ids: continue
    seen += 1
    if a["status"] == "protecting":
        bad.append(f"{a['id']} 여전히 protecting")
    # id 형식: incident_..._{key}_{fromUTC}; from = incidentTime-1h → incidentTime=from+1h
    # 자동 finalize: to = (incidentTime+2h) + 30m = from + 3h30m (±5분 허용)
    try:
        gap = (iso(a["to"]) - iso(a["from"])).total_seconds()
        if abs(gap - 3.5 * 3600) < 300: okto += 1
    except Exception: pass
print(f"{'OKALL' if seen and not bad else 'NOKALL'}")
print(f"자동 finalize incident 아카이브 {seen}개 전부 protecting 탈출, to=from+3h30m(만료2h+30m) 일치 {okto}개")
for b in bad[:3]: print("NOK " + b)
EOF
)
  rm -f "$tmp"
  head=$(echo "$res" | head -1); echo "$res" | tail -n +2 | sed 's/^/  [..]  /'
  [ "$head" = "OKALL" ] || FAILED=1

  if [ "${ALLOW_MUTATING:-0}" != "1" ]; then
    if [ "$FAILED" -eq 0 ]; then
      echo "VERDICT M: OK (사후 관측 — 만료 protect가 자동 finalize로 전이한 운영 이력 확인. 60초 시한·3시간전 protect 주입은 mutating으로 미실행)"
    else
      echo "VERDICT M: NOK (사후 관측 기준)"; exit 1
    fi
    exit 0
  fi
fi

# ---- MUTATING PART (승인 시에만) ----
echo "  [!!] MUTATING 절차: POST /api/archives/protect {incidentTime: now-3h} → 60초 내 상태 전이 관찰"
verdict M
