#!/usr/bin/env bash
# S. 재연결 경계 수신측 유실 방지 (gateway만 재기동, 브로커 상시가동).
#    clean=false + 고정 clientID → gateway 정지 구간 발행된 QoS2 alert를 브로커 세션 큐가
#    재기동 후 재전송 → POST /api/incidents로 forward되어 유실되지 않는다.
# spec: docs/spec/hw-gateway.md — 검증 단언 S · §회복력(persistent session)
#
# 전제(§S): mosquitto는 시나리오 내내 상시 가동한다(브로커 재기동 안 함). gateway만 정지→재기동.
#
# F2 결정화: 이전 판(sleep 없음)은 gateway stop→publish→start 사이 offline 창이 sub-second라
#   S 판정이 비결정적이었고, 밀린 stale alert가 나중 실행으로 flush돼 web-backend 부수효과가
#   누출됐다. 이제 명확한 offline 창을 확보한다:
#     - gateway 정지 후 sleep 3  : 브로커가 세션 offline 을 확정 인지
#     - alert publish 후 sleep 2 : QoS2 메시지가 세션 큐에 확정 적재
#   재기동 후 관측창을 (고정 sleep 대신) 토큰이 나타날 때까지 최대 30s 폴링으로 확대해
#   재연결+재전송+병렬 forward 완료를 결정적으로 포착한다.
set -uo pipefail
. "$(dirname "$0")/../lib-web.sh"

require_mutating

# ⚠️ 침습적: sentinel-hw-gateway 정지→재기동 (정지 구간 MQTT 수신 공백). 브로커는 계속 가동.
TAG="S$(date -u +%s)$$"
DEV="T-S1-$TAG"          # 로그에서 이 실행만 식별하기 위한 고유 deviceId
ALERT="A-$TAG"          # 고유 alertId (incident dedup 키)
SITE="site1"

# 종료 시 복원: 어떤 실패 경로에서도 gateway를 기동하고 healthz 200/ok 회복을 폴링(라이브 healthy 보장).
restore() {
  docker start sentinel-hw-gateway >/dev/null 2>&1 || true
  for _ in $(seq 1 30); do
    [ "$(bcode "$HWGW/healthz")" = 200 ] && break
    sleep 1
  done
}

T0=$(date -u +%Y-%m-%dT%H:%M:%S)   # gateway 로그 커서 (--since)
docker stop sentinel-hw-gateway >/dev/null
trap restore EXIT
sleep 3   # F2: 브로커가 세션 offline 을 확정 인지하도록 명확한 offline 창 확보

# gateway 오프라인 동안 alert 발행 (QoS2). 브로커는 persistent session이면 세션 큐에 적재.
docker exec sentinel-mosquitto mosquitto_pub -h localhost -q 2 \
  -t "safety/$SITE/alert" \
  -m "{\"deviceId\":\"$DEV\",\"siteId\":\"$SITE\",\"type\":\"fall\",\"alertId\":\"$ALERT\",\"timestamp\":\"$(date -u +%Y-%m-%dT%H:%M:%SZ)\"}" \
  || nok "브로커 발행 실패 (mosquitto 미가동?)"
sleep 2   # F2: QoS2 메시지가 세션 큐에 확정 적재되도록 대기

# gateway 재기동 → 재접속 시 세션 큐 미전달 QoS2 재전송 → forward
docker start sentinel-hw-gateway >/dev/null; trap restore EXIT

# F2: 관측창 확대 — 토큰이 gateway 로그에 나타날 때까지 최대 30s 폴링(재연결+재전송+forward 완료).
seen=0
for _ in $(seq 1 30); do
  if docker logs --since "$T0" sentinel-hw-gateway 2>&1 | grep -q -e "$DEV" -e "$ALERT"; then seen=1; break; fi
  sleep 1
done

logs=$(docker logs --since "$T0" sentinel-hw-gateway 2>&1)
echo "--- gateway logs (--since $T0) tail ---"
printf '%s\n' "$logs" | tail -n 20

if [ "$seen" = 1 ]; then
  ok "재연결 경계 세션 큐 재전송 관측 (토큰 $DEV/$ALERT) → 수신측 alert 유실 방지 성립"
else
  nok "재기동 후 세션 큐 재전송 미관측 — gateway가 clean=false/persistent session 미구성 추정 → RED"
fi
