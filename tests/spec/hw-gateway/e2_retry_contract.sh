#!/usr/bin/env bash
# E2 (Outbound 재시도/미재시도 계약 — spec §Outbound):
#   web-backend: transport/5xx에 최대 3회 재시도(지수백오프 1s×2 ±25% jitter), 4xx 미재시도,
#                2xx만 성공. notifier: 재시도 없음(1회).
# spec: docs/spec/hw-gateway.md — §출력 Outbound HTTP · ⚠리뷰 #1 · 단언 E
#
# 격리 하네스로 목 서버 요청 도달 횟수를 실측한다(라이브 미접촉). 세 케이스 각각 전용
# stack(broker+notifier목+web목+gateway)으로 격리 실행 — 상호 카운트 오염 제거:
#   (a) web 목 500       → web /api/incidents 도달 ≈4 (최초1+재시도3), notifier 1
#   (b) web 목 400       → web /api/incidents 도달 == 1 (4xx 미재시도),  notifier 1
#   (c) notifier 목 500  → notifier /api/notify 도달 == 1 (재시도 없음), web 1(2xx 성공)
set -uo pipefail
cd "$(dirname "$0")"
. ./lib-gw-isolated.sh
. ../lib-web.sh

require_mutating

iso_preflight || nok "격리 이미지($GW_ISO_IMG) 부재·빌드 실패"
iso_init
trap iso_cleanup EXIT

FAIL=0

# run_case <label> <notif-status> <web-status> — 전용 stack 기동, alert 1건 발행, 카운트 반환.
# 결과를 전역 RC_NOTIF / RC_INC 에 채운다.
run_case() {
  local label="$1" nstat="$2" wstat="$3"
  local B="mosq-$label-$ISO_TAG" N="mockn-$label-$ISO_TAG" W="mockw-$label-$ISO_TAG" G="gw-$label-$ISO_TAG"
  iso_broker "$B"
  iso_mock "$N" "$nstat"
  iso_mock "$W" "$wstat"
  sleep 1
  iso_gw "$G" "$B" "http://$N:8080" "http://$W:8080"
  if ! iso_wait_healthy "$G" 40; then docker logs "$G" 2>&1 | tail -15; nok "[$label] 격리 gateway healthy 실패"; fi
  local ts; ts=$(date -u +%Y-%m-%dT%H:%M:%SZ)
  iso_pub "$B" 2 "safety/site1/alert" \
    "{\"deviceId\":\"D-$label\",\"siteId\":\"site1\",\"type\":\"fall\",\"alertId\":\"E2-$label-$ISO_TAG\",\"timestamp\":\"$ts\"}"
  sleep 12   # 재시도 백오프(≈1+2+4s) 소진 대기
  RC_NOTIF=$(iso_count "$N" 'REQ POST /api/notify')
  RC_INC=$(iso_count "$W" 'REQ POST /api/incidents')
  echo "[$label] notifier=$RC_NOTIF  web /api/incidents=$RC_INC"
}

# (a) web 5xx → web 재시도(≈4회), notifier 1회
run_case a5xx 200 500
if [ "$RC_NOTIF" = 1 ] && [ "${RC_INC:-0}" -ge 4 ]; then
  echo "  OK(a): web 5xx 재시도 ≈4(최초1+재시도3)=$RC_INC, notifier 미재시도=1"
else
  echo "  NOK(a): 기대 web>=4 & notifier==1 (got web=$RC_INC notifier=$RC_NOTIF)"; FAIL=1
fi

# (b) web 4xx → web 미재시도(정확히 1회)
run_case b4xx 200 400
if [ "$RC_NOTIF" = 1 ] && [ "$RC_INC" = 1 ]; then
  echo "  OK(b): web 4xx 미재시도=1, notifier=1"
else
  echo "  NOK(b): 기대 web==1 & notifier==1 (got web=$RC_INC notifier=$RC_NOTIF)"; FAIL=1
fi

# (c) notifier 5xx → notifier 미재시도(정확히 1회), web 2xx 성공 1회
run_case c5xx 500 200
if [ "$RC_NOTIF" = 1 ] && [ "$RC_INC" = 1 ]; then
  echo "  OK(c): notifier 5xx 미재시도=1, web 2xx=1"
else
  echo "  NOK(c): 기대 notifier==1 & web==1 (got notifier=$RC_NOTIF web=$RC_INC)"; FAIL=1
fi

[ "$FAIL" = 0 ] && ok "재시도 계약 3케이스 성립: web 5xx≈4회·web 4xx 1회·notifier 1회" \
  || nok "재시도/미재시도 계약 위반 (위 케이스별 got 참조)"
