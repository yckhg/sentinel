#!/usr/bin/env bash
# P. 녹화 보호 요청 병렬성 (알림 완료를 대기하지 않음): 유효 이벤트 주입 시, notifier 로그의
#    녹화 보호 요청 기록 타임스탬프가 전 연락처 dispatch 요약 로그(그리고 최초 채널 시도 완료)
#    **이전**이어야 한다. 보호 요청은 알림 dispatch 와 병렬·즉시 트리거되며 발송 완료를 대기하지
#    않는다(§출력 6). 보호가 발송 완료 뒤에 찍히면 = 발송을 기다린 것 → 위반.
# spec: docs/spec/notifier.md — 검증 단언 P (§출력 6 병렬·즉시)
#
# 관측: docker logs -t 의 컨테이너 타임스탬프(나노초)로 [archive] 보호 수락 라인과
#   [notify] Dispatch complete 요약 라인의 순서를 비교한다. 스펙은 '느린 채널'로 창을 넓히길
#   권하지만, 현 구현이 보호를 요약 뒤에 호출하면 채널 속도와 무관하게 보호_ts > 요약_ts 로
#   위반이 드러난다(느린-채널 픽스처는 창을 넓힐 뿐 판정 방향을 바꾸지 않음 — 헤더로 문서화).
# 판정: OK=exit0 (보호_ts < 요약_ts), NOK=exit1 (보호_ts >= 요약_ts, 발송 완료 대기), SKIPPED=exit2.
set -uo pipefail
. "$(dirname "$0")/../lib-web.sh"
NOTIFIER=${NOTIFIER:-http://notifier:8080}

require_mutating

# 보호 요청이 실제로 발생하려면 활성 카메라 ≥1 + recording 도달 가능(보호 '수락' 로그) 필요.
cams=$(db_query "SELECT COUNT(*) FROM cameras WHERE enabled=1;" 2>/dev/null || echo 0)
[ "${cams:-0}" -ge 1 ] || skip "활성 카메라 0대 — 보호 요청이 생략되어 순서 관측 불가"
# dispatch 요약이 의미를 가지려면 연락처 ≥1 (발송 작업이 존재해야 보호와의 선후가 유의미).
ncontacts=$(db_query "SELECT COUNT(*) FROM contacts WHERE deleted_at IS NULL;" 2>/dev/null \
  || db_query "SELECT COUNT(*) FROM contacts;" 2>/dev/null || echo 0)
[ "${ncontacts:-0}" -ge 1 ] || skip "연락처 0건 — dispatch 요약이 공허해 순서 비교 불가"

SINCE=$(date -u +%Y-%m-%dT%H:%M:%SZ)
TS=$(date -u +%Y-%m-%dT%H:%M:%SZ)
bcode -X POST "$NOTIFIER/api/notify" -H 'Content-Type: application/json' \
  -d "{\"siteId\":\"site1\",\"deviceId\":\"TEST-P1\",\"type\":\"gas_leak\",\"timestamp\":\"$TS\",\"test\":true}" >/dev/null
sleep 10

logs=$(docker logs -t --since "$SINCE" sentinel-notifier 2>&1)
# 각 라인의 1번째 필드 = 컨테이너 타임스탬프(RFC3339Nano). 첫 매칭을 취한다.
protect_ts=$(printf '%s' "$logs" | grep -E "\[archive\] Protect request accepted for incident" | head -1 | awk '{print $1}')
summary_ts=$(printf '%s' "$logs" | grep -E "\[notify\] Dispatch complete:" | head -1 | awk '{print $1}')
echo "protect_ts=${protect_ts:-<none>} summary_ts=${summary_ts:-<none>}"

[ -n "$protect_ts" ] || skip "[archive] 보호 수락 로그 없음 (recording 다운/보호 생략) — 순서 관측 불가"
[ -n "$summary_ts" ] || skip "[notify] Dispatch complete 요약 로그 없음 — 순서 관측 불가"

pe=$(date -d "$protect_ts" +%s%N 2>/dev/null) || nok "protect_ts 파싱 실패: $protect_ts"
se=$(date -d "$summary_ts" +%s%N 2>/dev/null) || nok "summary_ts 파싱 실패: $summary_ts"
echo "protect_ns=$pe summary_ns=$se"

[ "$pe" -lt "$se" ] || nok "보호 요청이 dispatch 요약 이후에 찍힘 (protect_ts >= summary_ts) — 발송 완료를 대기함(비병렬)"
ok "보호 요청이 dispatch 요약보다 앞섬 — 알림 완료를 대기하지 않는 병렬·즉시 트리거"
