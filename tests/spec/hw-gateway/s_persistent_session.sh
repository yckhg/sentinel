#!/usr/bin/env bash
# S. 재연결 경계 수신측 유실 방지 (gateway만 재기동, 브로커 상시가동).
#    clean=false + 고정 clientID → gateway 정지 구간 발행된 QoS2 alert를 브로커 세션 큐가
#    재기동 후 재전송 → POST /api/incidents로 forward되어 유실되지 않는다.
# spec: docs/spec/hw-gateway.md — 검증 단언 S · §회복력(persistent session)
#
# 전제(§S): mosquitto는 시나리오 내내 상시 가동한다(브로커 재기동 안 함). gateway만 정지→재기동.
# 관측: 재기동 후 gateway 로그(--since)에서 재전송된 alert의 고유 토큰이 처리/forward됨을 확인한다
#       (§검증: "web-backend incident count 또는 hw-gateway forward log"). clean=false/persistent
#       session이 미구성(현 배포 clean=true 추정)이면 재전송이 없어 토큰 미관측 → RED.
set -uo pipefail
. "$(dirname "$0")/../lib-web.sh"

require_mutating

# ⚠️ 침습적: sentinel-hw-gateway 정지→재기동 (정지 구간 MQTT 수신 공백). 브로커는 계속 가동.
TAG="S$(date -u +%s)$$"
DEV="T-S1-$TAG"          # 로그에서 이 실행만 식별하기 위한 고유 deviceId
ALERT="A-$TAG"          # 고유 alertId (incident dedup 키)
SITE="site1"

T0=$(date -u +%Y-%m-%dT%H:%M:%S)   # gateway 로그 커서 (--since)
docker stop sentinel-hw-gateway >/dev/null
trap 'docker start sentinel-hw-gateway >/dev/null 2>&1 || true' EXIT

# gateway 오프라인 동안 alert 발행 (QoS2). 브로커는 persistent session이면 세션 큐에 적재.
docker exec sentinel-mosquitto mosquitto_pub -h localhost -q 2 \
  -t "safety/$SITE/alert" \
  -m "{\"deviceId\":\"$DEV\",\"siteId\":\"$SITE\",\"type\":\"fall\",\"alertId\":\"$ALERT\",\"timestamp\":\"$(date -u +%Y-%m-%dT%H:%M:%SZ)\"}" \
  || nok "브로커 발행 실패 (mosquitto 미가동?)"

# gateway 재기동 → 재접속 시 세션 큐 미전달 QoS2 재전송 → forward
docker start sentinel-hw-gateway >/dev/null; trap - EXIT
sleep 15   # 재연결 + 재전송 + 병렬 forward 완료 대기

logs=$(docker logs --since "$T0" sentinel-hw-gateway 2>&1)
echo "--- gateway logs (--since $T0) tail ---"
printf '%s\n' "$logs" | tail -n 20

if printf '%s' "$logs" | grep -q -e "$DEV" -e "$ALERT"; then
  ok "재연결 경계 세션 큐 재전송 관측 (토큰 $DEV/$ALERT) → 수신측 alert 유실 방지 성립"
else
  nok "재기동 후 세션 큐 재전송 미관측 — gateway가 clean=false/persistent session 미구성 추정 → RED"
fi
