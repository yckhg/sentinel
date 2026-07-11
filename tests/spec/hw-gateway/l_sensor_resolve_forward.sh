#!/usr/bin/env bash
# L. 센서 해소 forward: sensor_button → POST /api/incidents/{id}/resolve-from-sensor 를
#    동일 페이로드로 정확히 1회. unknown kind → 어떤 forward도 없음.
# spec: docs/spec/hw-gateway.md — 검증 단언 L · §alert/resolved 양방향 동기화
#
# F6 강화: 이전 판은 `grep -qi "resolve"`로 판정해 토픽 문자열 `alert/resolved`(모든 수신 로그)에도
#   매칭 → 실제 outbound 호출 없이도 통과 가능했고, "1회 수신"도 미검증이었다. 여기서는 격리
#   web-backend 목의 요청 PATH를 실측해 `resolve-from-sensor` **정확 매칭 + 정확히 1회**를 확인한다.
#   격리 하네스(전용 broker + web 목 + 격리 gateway)로 **라이브 web-backend/브로커 미접촉**.
set -uo pipefail
cd "$(dirname "$0")"
. ./lib-gw-isolated.sh
. ../lib-web.sh

require_mutating

iso_preflight || nok "격리 이미지($GW_ISO_IMG) 부재·빌드 실패"
iso_init
trap iso_cleanup EXIT

B="mosq-$ISO_TAG"; W="mockw-$ISO_TAG"; G="gw-$ISO_TAG"
iso_broker "$B"
iso_mock "$W" 200          # web-backend 목: resolve-from-sensor 200
sleep 1
iso_gw "$G" "$B" "http://$W:8080" "http://$W:8080"
iso_wait_healthy "$G" 40 || { docker logs "$G" 2>&1 | tail -20; nok "격리 gateway healthy 실패"; }

now=$(date -u +%Y-%m-%dT%H:%M:%SZ)

# 1) sensor_button 해소 → resolve-from-sensor 1회 forward 기대 (incidentId=0)
iso_pub "$B" 1 "safety/site1/alert/resolved" \
  "{\"incidentId\":0,\"siteId\":\"site1\",\"resolvedAt\":\"$now\",\"resolvedBy\":{\"kind\":\"sensor_button\",\"id\":\"T-L1\",\"label\":\"reset\"}}"
sleep 4

N1=$(iso_count "$W" 'REQ POST /api/incidents/0/resolve-from-sensor')
echo "sensor_button → resolve-from-sensor 수신 = $N1 (기대 1)"
[ "$N1" = 1 ] || nok "sensor_button 해소가 resolve-from-sensor 를 정확히 1회 forward하지 않음 (got=$N1)"

# 2) unknown kind → 어떤 forward도 없어야 함 (누적 카운트 불변)
iso_pub "$B" 1 "safety/site1/alert/resolved" \
  "{\"incidentId\":0,\"siteId\":\"site1\",\"resolvedAt\":\"$now\",\"resolvedBy\":{\"kind\":\"unknown\",\"id\":\"x\",\"label\":\"x\"}}"
sleep 4

N2=$(iso_count "$W" 'REQ POST /api/incidents/0/resolve-from-sensor')
echo "unknown kind 발행 후 누적 resolve-from-sensor = $N2 (기대 여전히 1)"
echo "--- gateway 로그 ---"; docker logs "$G" 2>&1 | grep -E 'ALERT-RESOLVED' | tail -n 8

if [ "$N2" = 1 ]; then
  ok "sensor_button → resolve-from-sensor 정확 1회; unknown kind → forward 없음 (라이브 미접촉)"
else
  nok "unknown kind가 forward됨 또는 카운트 이상 (누적=$N2, 기대 1)"
fi
