#!/usr/bin/env bash
# F2 (dedup NEGATIVE path — spec §alert 처리 2, resolved review #4):
#   alertId 캐시 등록은 web-backend forward 2xx 성공 이후에만 수행한다. forward가 최종
#   실패(전송오류/5xx 재시도 소진)하면 alertId를 등록하지 않아, 동일 alertId 재전송이
#   다시 forward되어 유실을 복구한다. (positive dedup은 f_dedup.sh가 커버.)
# spec: docs/spec/hw-gateway.md — §alert 처리 2 · ⚠리뷰 #4(해소) · 단언 F
#
# 격리 하네스: 전용 broker + web-backend 목(항상 500) + notifier 목(200) + 격리 gateway.
#   라이브 web-backend/notifier/broker 미접촉. 동일 alertId alert 2회 발행 →
#   dedup이 미발동해야 함(등록이 2xx 이후이므로 500이면 미등록):
#     - notifier 목 수신 == 2 (매번 forward — dedup 미발동의 결정적 판별자)
#     - web-backend 목 /api/incidents 수신 > 4 (두 번째 발행도 재시도까지 forward)
#   만약 (회귀로) 등록이 forward 성공 이전이면 2회차가 dedup되어 notifier==1 → NOK.
set -uo pipefail
cd "$(dirname "$0")"
. ./lib-gw-isolated.sh
. ../lib-web.sh

require_mutating   # 컨테이너 다수 기동 — 설계자 승인 게이트(라이브 미접촉이나 리소스 사용)

iso_preflight || nok "격리 이미지($GW_ISO_IMG) 부재·빌드 실패 — services/hw-gateway 빌드 필요"
iso_init
trap iso_cleanup EXIT

B="mosq-$ISO_TAG"; N="mockn-$ISO_TAG"; W="mockw-$ISO_TAG"; G="gw-$ISO_TAG"
iso_broker "$B"
iso_mock "$N" 200      # notifier 목: 성공 (재시도 없음이므로 상태 무관)
iso_mock "$W" 500      # web-backend 목: 항상 500 → forward 최종 실패
sleep 1
iso_gw "$G" "$B" "http://$N:8080" "http://$W:8080"
iso_wait_healthy "$G" 40 || { docker logs "$G" 2>&1 | tail -20; nok "격리 gateway healthy 실패"; }

ts=$(date -u +%Y-%m-%dT%H:%M:%SZ)
AID="RECOV-$ISO_TAG"
MSG="{\"deviceId\":\"D-F2\",\"siteId\":\"site1\",\"type\":\"fall\",\"alertId\":\"$AID\",\"timestamp\":\"$ts\"}"

# 1회차: web forward 4회(최초1+재시도3, 각 ~1/2/4s 백오프) 실패 → alertId 미등록
iso_pub "$B" 2 "safety/site1/alert" "$MSG"
sleep 12   # 재시도 백오프 소진(≈1+2+4s + 여유) 대기
# 2회차: 동일 alertId. dedup 미발동이면 다시 forward.
iso_pub "$B" 2 "safety/site1/alert" "$MSG"
sleep 12

NOT=$(iso_count "$N" 'REQ POST /api/notify')
INC=$(iso_count "$W" 'REQ POST /api/incidents')
echo "notifier=$NOT  web-backend /api/incidents=$INC (기대: notifier=2, incidents>4)"
echo "--- gateway 로그(dedup 판정) ---"
docker logs "$G" 2>&1 | grep -E 'Duplicate alertId|All retries exhausted|Web-backend response' | tail -n 12

if [ "$NOT" = 2 ] && [ "${INC:-0}" -gt 4 ]; then
  ok "forward 최종 실패 시 alertId 미등록 → 동일 alertId 재전송이 다시 forward(유실복구): notifier=2, incidents=$INC"
else
  nok "dedup 부정경로 위반 — notifier=$NOT(기대2) incidents=$INC(기대>4). notifier==1이면 2회차가 부당하게 dedup됨(등록이 forward 성공 이전 = 리뷰#4 회귀)"
fi
