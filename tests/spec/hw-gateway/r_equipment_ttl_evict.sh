#!/usr/bin/env bash
# R. TTL eviction (C와 격리, 기동 불변식 준수): HEARTBEAT_TIMEOUT_SEC=3, EQUIPMENT_EVICT_TTL_SEC=10
#    인스턴스에서 장비 등록 후 TTL+주기 여유만큼 재수신 중단 → status에서 항목 제거(제거지 dead=false 아님).
#    TTL 이내 재수신 시 항목 유지.
# spec: docs/spec/hw-gateway.md — 검증 단언 R · §장비 스토어 보존 상한·eviction
#
# 격리: C(dead 유지, 대TTL)와 상호오염 금지 → 별도 전용 인스턴스. clientID 고정 충돌 회피를 위해
#       전용 throwaway 브로커 + gateway 격리(§q 스크립트 주석과 동일 이유). 기동 불변식
#       (TTL 10 > HEARTBEAT 3) 준수 → 강제 86400 대체가 일어나지 않아 소TTL 제거를 관측 가능.
set -uo pipefail
. "$(dirname "$0")/../lib-web.sh"

require_mutating

IMG=${HWGW_IMG:-sentinel-hw-gateway:latest}
docker image inspect "$IMG" >/dev/null 2>&1 \
  || skip "hw-gateway 이미지($IMG) 부재 — feat/hw-gateway 빌드 후 검증 (verifier 이미지 보유 시 green)"

SFX="r$$"
BROKER="sentinel-ttl-broker-$SFX"
GW="sentinel-ttl-gw-$SFX"
cleanup() { docker rm -f "$GW" "$BROKER" >/dev/null 2>&1 || true; }
trap cleanup EXIT

docker run -d --network "$NET" --name "$BROKER" eclipse-mosquitto:2 \
  sh -c 'printf "listener 1883 0.0.0.0\nallow_anonymous true\n" > /mosquitto/config/mosquitto.conf; exec mosquitto -c /mosquitto/config/mosquitto.conf' \
  >/dev/null || nok "throwaway 브로커 기동 실패"
sleep 2

docker run -d --network "$NET" --name "$GW" \
  -e MQTT_BROKER_URL="tcp://$BROKER:1883" \
  -e HEARTBEAT_TIMEOUT_SEC=3 \
  -e EQUIPMENT_EVICT_TTL_SEC=10 \
  -e WEB_BACKEND_URL="http://127.0.0.1:9" \
  -e NOTIFIER_URL="http://127.0.0.1:9" \
  "$IMG" >/dev/null || nok "throwaway gateway 기동 실패"

ready=0
for i in $(seq 1 20); do
  c=$(docker run --rm --network "$NET" "$CURL_IMG" -s -o /dev/null -w '%{http_code}' \
        --max-time 3 "http://$GW:8080/healthz" 2>/dev/null)
  [ "$c" = 200 ] && { ready=1; break; }
  sleep 1
done
[ "$ready" = 1 ] || nok "throwaway gateway healthz 200 미도달 — 브로커 연결/구독 실패"

status() { docker run --rm --network "$NET" "$CURL_IMG" -s --max-time 5 "http://$GW:8080/api/equipment/status"; }
hb() { docker exec "$BROKER" mosquitto_pub -h localhost -q 1 -t "safety/site1/heartbeat" \
       -m "{\"deviceId\":\"$1\",\"siteId\":\"site1\",\"alertState\":\"none\",\"timestamp\":\"$(date -u +%Y-%m-%dT%H:%M:%SZ)\"}"; }

# (1) TTL 초과 제거: T-R1 등록 → 재수신 중단 → TTL(10) + eviction 주기 여유(~10s) 후 항목 제거
hb "T-R1"; sleep 2
status | grep -q '"T-R1"' || nok "T-R1 초기 등록 실패"
echo "T-R1 등록 확인. TTL(10s) 초과 대기..."
sleep 22
after=$(status); echo "제거 후 status=$after"
printf '%s' "$after" | grep -q '"T-R1"' && nok "T-R1 미제거 — TTL eviction 불성립(dead=false로만 남으면 RED, 항목 자체가 사라져야 함)"
echo "T-R1 TTL 제거 확인."

# (2) TTL 이내 재수신 시 유지: T-R2 등록 후 7s(<TTL)마다 재수신 → 마지막 수신 기준 TTL 미만이라 유지
hb "T-R2"; sleep 7; hb "T-R2"; sleep 7   # 총 14s 경과했으나 마지막 재수신 후 7s(<10)
status | grep -q '"T-R2"' && ok "TTL eviction 성립: 초과 제거 + TTL 이내 재수신 시 항목 유지 확인" \
  || nok "T-R2가 TTL 이내 재수신에도 제거됨 — 재수신 시 last-seen 갱신 실패"
