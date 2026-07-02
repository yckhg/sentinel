#!/usr/bin/env bash
# 단언 K. 시간 범위 병합 — 연속 세그먼트만 있으면 timeRanges 길이 1,
#   20초 이상 공백을 만들면 길이 2.
# SKIP(규정 절차 일부): "중간 파일 삭제"로 공백을 만드는 것은 MUTATING — 실행 금지.
# 사후 판정(READ-ONLY): 프로덕션 데이터에 병합 케이스(≤15초 간격 연속 세그먼트 다수)와
#   분리 케이스(>15초 공백)가 자연 존재 — 파일 목록으로 기대 timeRanges를 계산해
#   GET /api/recordings/{k} 응답과 대조한다 (동일 계약을 동일 입력으로 검증).
. "$(dirname "$0")/common.sh"

k=$(active_key)
[ -n "$k" ] || { echo "VERDICT K: NOK — 활성 스트림 없음"; exit 1; }

tmp=$(mktemp -d)
http_get "$REC/api/recordings/$k" "$tmp/api.json"
[ "${STATUS:-}" = "200" ] && ok "GET /api/recordings/$k 200" || nok "status=${STATUS:-none}"
rexec "ls $RECORDINGS_DIR/$k" | grep -E '^[0-9]{8}_[0-9]{6}\.ts$' > "$tmp/files.txt"

res=$(python3 - "$tmp" <<'EOF'
import json, sys, datetime as dt
tmp = sys.argv[1]
SEG, GAP = dt.timedelta(seconds=10), dt.timedelta(seconds=15)
files = sorted(dt.datetime.strptime(l.strip()[:-3], "%Y%m%d_%H%M%S") for l in open(f"{tmp}/files.txt"))
exp = []  # 기대 ranges: (start, end) — end = last_start + 10s, 간격(다음 시작 - 이전 끝) > 15s 에서 분리
s = p = files[0]
for t in files[1:]:
    if t - (p + SEG) > GAP:
        exp.append((s, p + SEG)); s = t
    p = t
exp.append((s, p + SEG))

api = json.load(open(f"{tmp}/api.json"))
iso = lambda x: dt.datetime.fromisoformat(x.replace("Z", "+00:00")).replace(tzinfo=None)
got = [(iso(r["start"]), iso(r["end"])) for r in api["timeRanges"]]

lines, okall = [], True
lines.append(f"세그먼트 {len(files)}개 → 기대 range {len(exp)}개, API {len(got)}개")
multi = sum(1 for a, b in got if b - a > SEG)
if len(got) >= 2 and multi >= 1:
    lines.append(f"ok 병합 케이스(다세그먼트 range {multi}개)와 분리 케이스(range {len(got)}개) 모두 관측")
else:
    okall = False; lines.append("NOK 병합/분리 케이스 관측 실패")
# 대조 (마지막 range 끝은 진행 중 녹화로 ±30s 허용, range 수는 ±1 허용 — 조회 간 레이스)
if abs(len(exp) - len(got)) > 1:
    okall = False; lines.append(f"NOK range 수 불일치: 기대 {len(exp)} vs API {len(got)}")
else:
    n = min(len(exp), len(got))
    bad = [i for i in range(n - 1) if exp[i][0] != got[i][0] or abs((exp[i][1] - got[i][1]).total_seconds()) > 1]
    if exp[n-1][0] != got[n-1][0] or abs((exp[n-1][1] - got[n-1][1]).total_seconds()) > 30:
        bad.append(n - 1)
    if bad:
        okall = False
        for i in bad[:3]:
            lines.append(f"NOK range[{i}] 기대 {exp[i][0]}~{exp[i][1]} vs API {got[i][0]}~{got[i][1]}")
    else:
        lines.append(f"ok 전 range 경계 일치 (15초 병합 규칙 + 세그먼트 10초 가정)")
print("OKALL" if okall else "NOKALL"); print("\n".join(lines))
EOF
)
rm -rf "$tmp"
head=$(echo "$res" | head -1); echo "$res" | tail -n +2 | sed 's/^/  [..]  /'
[ "$head" = "OKALL" ] || FAILED=1

if [ "$FAILED" -eq 0 ]; then
  echo "VERDICT K: OK (사후 관측 — 자연 데이터로 병합·분리 양 케이스 검증. '중간 파일 삭제' 절차는 mutating으로 미실행)"
else
  echo "VERDICT K: NOK"; exit 1
fi
