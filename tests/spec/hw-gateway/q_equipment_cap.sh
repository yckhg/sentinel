#!/usr/bin/env bash
# Q. 장비 스토어 상한 (무한 증가 금지): EQUIPMENT_MAX_DEVICES=5 인스턴스에 20개 distinct
#    deviceId heartbeat → GET /api/equipment/status 길이 ≤ 5, least-recently-seen 축출.
# spec: docs/spec/hw-gateway.md — 검증 단언 Q · §장비 스토어 보존 상한·eviction
#
# 격리: 라이브 인스턴스는 기본 cap(1000)이라 관측 불가. 또 gateway clientID는 고정
#       (sentinel-hw-gateway)이라 라이브 브로커에 소인스턴스를 붙이면 clientID 충돌(takeover)이
#       난다. 따라서 전용 throwaway 브로커 + throwaway gateway를 sentinel-net에 띄워 완전 격리한다.
#       forward 대상(web-backend/notifier)은 blackhole로 지정해 프로덕션 오염을 막는다
#       (devices/seen은 fire-and-forget이라 실패해도 무해).
set -uo pipefail
. "$(dirname "$0")/../lib-web.sh"

require_mutating

IMG=${HWGW_IMG:-sentinel-hw-gateway:latest}
docker image inspect "$IMG" >/dev/null 2>&1 \
  || skip "hw-gateway 이미지($IMG) 부재 — feat/hw-gateway 빌드 후 검증 (verifier 이미지 보유 시 green)"

SFX="q$$"
BROKER="sentinel-cap-broker-$SFX"
GW="sentinel-cap-gw-$SFX"
cleanup() { docker rm -f "$GW" "$BROKER" >/dev/null 2>&1 || true; }
trap cleanup EXIT

docker run -d --network "$NET" --name "$BROKER" eclipse-mosquitto:2 \
  sh -c 'printf "listener 1883 0.0.0.0\nallow_anonymous true\n" > /mosquitto/config/mosquitto.conf; exec mosquitto -c /mosquitto/config/mosquitto.conf' \
  >/dev/null || nok "throwaway 브로커 기동 실패"
sleep 2

docker run -d --network "$NET" --name "$GW" \
  -e MQTT_BROKER_URL="tcp://$BROKER:1883" \
  -e EQUIPMENT_MAX_DEVICES=5 \
  -e WEB_BACKEND_URL="http://127.0.0.1:9" \
  -e NOTIFIER_URL="http://127.0.0.1:9" \
  "$IMG" >/dev/null || nok "throwaway gateway 기동 실패"

# gateway가 브로커에 연결·구독 성립(healthz 200)할 때까지 대기
ready=0
for i in $(seq 1 20); do
  c=$(docker run --rm --network "$NET" "$CURL_IMG" -s -o /dev/null -w '%{http_code}' \
        --max-time 3 "http://$GW:8080/healthz" 2>/dev/null)
  [ "$c" = 200 ] && { ready=1; break; }
  sleep 1
done
[ "$ready" = 1 ] || nok "throwaway gateway healthz 200 미도달 — 브로커 연결/구독 실패"

# cap(5)을 초과하는 20개 distinct deviceId를 순차 발행 (T-Q-01 이 최오래, T-Q-20 이 최근)
for n in $(seq -w 1 20); do
  docker exec "$BROKER" mosquitto_pub -h localhost -q 1 \
    -t "safety/site1/heartbeat" \
    -m "{\"deviceId\":\"T-Q-$n\",\"siteId\":\"site1\",\"alertState\":\"none\",\"timestamp\":\"$(date -u +%Y-%m-%dT%H:%M:%SZ)\"}"
  sleep 0.3
done
sleep 3

body=$(docker run --rm --network "$NET" "$CURL_IMG" -s --max-time 5 "http://$GW:8080/api/equipment/status")
cnt=$(printf '%s' "$body" | grep -o '"deviceId"' | wc -l | tr -d ' ')
echo "status length=$cnt (cap=5)"
[ "$cnt" -le 5 ] || nok "장비 스토어 상한 초과: length=$cnt > 5 — 무한 증가 금지 불변식 위반(RED)"
printf '%s' "$body" | grep -q '"T-Q-20"' || nok "최근 수신 장비(T-Q-20) 미보존 — LRU 보존 실패"
printf '%s' "$body" | grep -q '"T-Q-01"' && nok "최오래 장비(T-Q-01) 미축출 — least-recently-seen eviction 실패"
ok "상한 eviction 성립: length=$cnt ≤ 5, 최근 장비 보존·최오래 장비 축출 확인"
