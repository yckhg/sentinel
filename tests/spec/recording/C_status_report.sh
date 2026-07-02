#!/usr/bin/env bash
# 단언 C. 상태 보고 —
#   (1) [READ-ONLY] /api/status 의 k 항목: status=="recording", startedAt 이 RFC3339.
#   (2) [MUTATING — 기본 SKIP] 스트림 발행 중단 시 1.5×FFMPEG_TIMEOUT+15초 내
#       reconnecting/disconnected 전이 + 로그 확인.
# SKIP: (2)는 프로덕션 증거 녹화 스트림의 발행 중단을 유발하므로 실행 금지
#       (ALLOW_MUTATING=1 + 설계자 승인 시에만 수동 절차 안내).
. "$(dirname "$0")/common.sh"

body=$(mktemp)
http_get "$REC/api/status" "$body"
[ "${STATUS:-}" = "200" ] && ok "GET /api/status 200" || nok "status=${STATUS:-none}"

res=$(python3 - "$body" <<'EOF'
import json, sys, datetime
entries = json.load(open(sys.argv[1]))
rec = [e for e in entries if e.get("status") == "recording"]
if not rec:
    print("NOK: status==recording 항목 없음"); sys.exit(0)
for e in rec:
    sa = e.get("startedAt") or ""
    try:
        datetime.datetime.fromisoformat(sa.replace("Z", "+00:00"))
    except Exception:
        print(f"NOK: {e['streamKey']} startedAt='{sa}' RFC3339 아님"); sys.exit(0)
    allowed = {"recording", "reconnecting", "disconnected"}
    if e["status"] not in allowed:
        print(f"NOK: 미지 status {e['status']}"); sys.exit(0)
keys = ", ".join(f"{e['streamKey']}(startedAt={e['startedAt']})" for e in rec)
print(f"OK: recording 항목 {len(rec)}개, startedAt RFC3339 유효 — {keys}")
EOF
)
rm -f "$body"
case "$res" in OK*) ok "$res";; *) nok "$res";; esac

if [ "${ALLOW_MUTATING:-0}" != "1" ]; then
  if [ "$FAILED" -eq 0 ]; then
    echo "VERDICT C: OK (부분 — 발행중단→reconnecting/disconnected 전이 검사는 mutating으로 SKIPPED, 설계자 승인 대기)"
  else
    echo "VERDICT C: NOK"; exit 1
  fi
  exit 0
fi

# ---- MUTATING PART (승인 시에만) ----
echo "  [!!] MUTATING: 스트림 발행 중단 후 아래를 관찰하시오:"
echo "       timeout=\$(docker exec $REC_CONTAINER printenv FFMPEG_TIMEOUT); 한도=1.5×timeout+15s 내"
echo "       /api/status 의 해당 key status ∈ {reconnecting,disconnected}"
echo "       docker logs $REC_CONTAINER | grep -E 'FFmpeg (output timeout|exited)'"
verdict C
