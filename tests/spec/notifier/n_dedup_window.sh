#!/usr/bin/env bash
# N. 동일 incident 중복 억제 (dedup): DEDUP_WINDOW_SECONDS 가 양수(기본 60)인 상태에서
#    동일 (siteId,deviceId,type) 이벤트를 윈도우 내에 연속 2회 주입하면, 두 요청 모두 200 이나
#    실제 연락처 발송(채널 시도 또는 시스템 알람 dispatch)은 **1회분만** 관측되고 2번째 이벤트는
#    중복 억제 로그가 남는다. (최초 이벤트는 절대 억제되지 않는다.)
# spec: docs/spec/notifier.md — 검증 단언 N (§출력 12)
#
# 이 게이트가 단일 런에서 관측하는 것: 윈도우 활성 시 [최초 1회 dispatch + 2번째 억제 로그].
# 단일 런에서 함께 단언하지 못하고 헤더로만 문서화하는 env 의존 분기:
#   - DEDUP_WINDOW_SECONDS=0 → 억제 비활성 → 동일 2회 주입 시 dispatch 2회분(별도 재구성 필요).
#   - test:true 이벤트는 dedup 대상에서 제외(판정·기록 모두 안 함) → 실제 위기 캐시 오염 방지.
#     그래서 본 단언은 반드시 test:false 실제 이벤트로 검증한다(→ mutating).
# 판정: OK=exit0, NOK=exit1 (2번째도 dispatch 되거나 최초가 억제), SKIPPED=exit2.
set -uo pipefail
. "$(dirname "$0")/../lib-web.sh"
NOTIFIER=${NOTIFIER:-http://notifier:8080}

# test:false 실제 이벤트 2회 → 실제 시스템 알람/보호 요청 유발. mutating 게이트.
require_mutating

getenv() {
  docker inspect sentinel-notifier --format "{{range .Config.Env}}{{println .}}{{end}}" 2>/dev/null \
    | grep -E "^$1=" | head -1 | cut -d= -f2-
}
DW=$(getenv DEDUP_WINDOW_SECONDS)
# 미설정이면 스펙 기본값 60(활성)으로 간주. 명시적 0 이면 억제 비활성 — 이 단언 관측 불가.
[ "${DW:-60}" = 0 ] && skip "DEDUP_WINDOW_SECONDS=0 (억제 비활성) — 윈도우 활성 분기 관측 불가"
echo "DEDUP_WINDOW_SECONDS=${DW:-<unset,기본60>}"

# 동일 (siteId,deviceId,type). deviceId 는 런마다 고유(직전 런 캐시와 무충돌)하되 2회 주입은 동일.
MARKER="DEDUP-$(date -u +%s)-$RANDOM"
SINCE=$(date -u +%Y-%m-%dT%H:%M:%SZ)
mk() { printf '{"siteId":"site1","deviceId":"%s","type":"gas_leak","timestamp":"%s"}' \
  "$MARKER" "$(date -u +%Y-%m-%dT%H:%M:%SZ)"; }

code1=$(bcode -X POST "$NOTIFIER/api/notify" -H 'Content-Type: application/json' -d "$(mk)")
code2=$(bcode -X POST "$NOTIFIER/api/notify" -H 'Content-Type: application/json' -d "$(mk)")
sleep 8
log=$(docker logs sentinel-notifier --since "$SINCE" 2>&1)

# 이 이벤트가 dispatch 로 진입했는지: '[notify] Fetched N contacts' 는 억제되지 않은 이벤트만 방출.
# (Received alert 는 accept 마다 찍히므로 2회 — dispatch 여부 판정엔 부적합.)
recv=$(printf '%s' "$log" | grep -c "device=$MARKER" || true)
dispatched=$(printf '%s' "$log" | grep -cE "\[notify\] Fetched [0-9]+ contacts" || true)
suppressed=$(printf '%s' "$log" | grep -ciE "duplicate|dedup|suppress|중복" || true)
echo "code1=$code1 code2=$code2 접수로그=$recv dispatch진입=$dispatched 억제로그=$suppressed"

[ "$code1" = 200 ] && [ "$code2" = 200 ] || nok "두 요청 모두 200 이어야 함 (code1=$code1 code2=$code2)"
[ "$dispatched" = 1 ] || nok "윈도우 내 동일 키인데 dispatch 진입이 ${dispatched}회 — 정확히 1회(최초)만 발송돼야 함"
[ "$suppressed" -ge 1 ] || nok "2번째 이벤트에 대한 중복 억제 로그가 없음"
ok "윈도우 활성: 최초 1회 dispatch + 2번째 억제 로그 ${suppressed}건"
