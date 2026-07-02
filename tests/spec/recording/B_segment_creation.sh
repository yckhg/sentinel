#!/usr/bin/env bash
# 단언 B. 세그먼트 생성 — 활성 RTMP 스트림 k에서 30초 대기 후
#   {RECORDINGS_DIR}/k/ 에 ^\d{8}_\d{6}\.ts$ 신규 파일 2개 이상,
#   최신 파일명 타임스탬프(UTC)와 현재 UTC 차 <= 30초.
# 보조 관측: 완결된 기존 세그먼트 길이 ~10초 (ffprobe).
# 실행 정책: READ-ONLY (기존 활성 스트림 관찰만; 스트림 발행/중단 유발 없음).
. "$(dirname "$0")/common.sh"

k=$(active_key)
if [ -z "$k" ]; then echo "VERDICT B: NOK — status=recording 인 활성 스트림 없음"; exit 1; fi
info "streamKey=$k"

before=$(rexec "ls $RECORDINGS_DIR/$k" | grep -E '^[0-9]{8}_[0-9]{6}\.ts$' | sort)
sleep 31
after=$(rexec "ls $RECORDINGS_DIR/$k" | grep -E '^[0-9]{8}_[0-9]{6}\.ts$' | sort)

new=$(comm -13 <(echo "$before") <(echo "$after") | wc -l)
[ "$new" -ge 2 ] && ok "30초 대기 후 신규 세그먼트 ${new}개 (>=2)" || nok "신규 세그먼트 ${new}개 (<2)"

latest=$(echo "$after" | tail -1)
now=$(rexec "date -u +%Y%m%d_%H%M%S")
diff=$(python3 - "$latest" "$now" <<'EOF'
import sys, datetime
f = datetime.datetime.strptime(sys.argv[1][:15], "%Y%m%d_%H%M%S")
n = datetime.datetime.strptime(sys.argv[2].strip(), "%Y%m%d_%H%M%S")
print(int((n - f).total_seconds()))
EOF
)
if [ "$diff" -le 30 ] && [ "$diff" -ge -10 ]; then
  ok "최신 파일($latest) ts와 현재 UTC 차 ${diff}s (<=30)"
else
  nok "최신 파일($latest) ts와 현재 UTC 차 ${diff}s (>30)"
fi

# 보조: 완결 세그먼트(최신 2개 제외) 길이가 ~10초인지
seg=$(echo "$after" | tail -3 | head -1)
dur=$(rexec "ffprobe -v error -show_entries format=duration -of csv=p=0 $RECORDINGS_DIR/$k/$seg" | tr -d '\r')
if python3 -c "import sys; d=float('${dur:-0}'); sys.exit(0 if 8.0 <= d <= 12.5 else 1)"; then
  ok "세그먼트 길이 ${dur}s ≈ 10s ($seg)"
else
  nok "세그먼트 길이 ${dur}s — 10초 분할 계약 위배 의심 ($seg)"
fi
verdict B
