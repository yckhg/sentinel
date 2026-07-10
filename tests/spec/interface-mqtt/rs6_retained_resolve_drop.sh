#!/usr/bin/env bash
# RS-6. retained resolve 드롭 (OK: retained 메시지가 incident를 자동 해소하지 않음)
# spec: docs/spec/interface-mqtt.md — 검증 단언 RS-6 (commit #24 f988d73 수신측 방어)
#
# 방법론 주의(중요): retained 슬롯이 이미 있는 토픽에 mosquitto_pub -r 을 "구독 중인"
#   hw-gateway 로 보내면 라이브 전달(retain 플래그=0)이라 정상 처리된다 — 이는 사람이 실제
#   누른 라이브 sensor resolve 와 동일해 결함이 아니며, 순진한 라이브-publish 테스트는
#   false-NOK 를 낸다. 수신측 방어(isRetainedMessage)는 오직 **재구독 시 retained replay
#   (retain=1)** 에서만 발동한다. 따라서 정식 시나리오는:
#     1) 브로커에 retained resolve 슬롯을 적재
#     2) 신규 open incident Y(occurred_at 최신 = incidentId:0 fallback 대상) 생성
#     3) hw-gateway 재시작 → safety/# 재구독 → 브로커가 retained 를 retain=1 로 replay
#     4) Y 가 resolved_at NULL 유지 + [RETAINED] 로그 → 사람 게이트 방어 유효(OK)
set -uo pipefail
db_query() {
  docker run --rm -v sentinel_db-data:/data:ro alpine:3.19 \
    sh -c 'apk add -q sqlite >/dev/null && sqlite3 -readonly /data/sentinel.db "$1"' sh "$1"
}
PUB="docker exec sentinel-mosquitto mosquitto_pub -h localhost"
TOPIC="safety/site1/alert/resolved"

# SKIP: mutating — 프로덕션 실행 금지 (retained 슬롯 적재/incident 생성/hw-gateway 재시작 유발).
if [ "${ALLOW_MUTATING:-0}" != "1" ]; then
  echo "SKIPPED (mutating — 설계자 승인 대기): ALLOW_MUTATING=1 로만 실행"
  exit 2
fi

# 종료 시 항상 retained 슬롯을 clear (빈 payload retain 발행) — 잔재 방지.
cleanup() { $PUB -r -n -t "$TOPIC" >/dev/null 2>&1 || true; }
trap cleanup EXIT

echo "--- 0. clear any pre-existing retained slot ---"
$PUB -r -n -t "$TOPIC"; sleep 1

echo "--- 1. leave a RETAINED sensor_button resolve in the broker (incidentId:0) ---"
$PUB -q 1 -r -t "$TOPIC" \
  -m "{\"incidentId\":0,\"siteId\":\"site1\",\"resolvedAt\":\"$(date -u +%Y-%m-%dT%H:%M:%SZ)\",\"resolvedBy\":{\"kind\":\"sensor_button\",\"id\":\"RS6-REPLAY\",\"label\":\"retained replay probe\"}}"
sleep 3

echo "--- 2. create a FRESH open incident Y (occurred_at 최신 → id=0 fallback 대상) ---"
aidY="RS6-Y-$(date +%s)"; tsY=$(date -u +%Y-%m-%dT%H:%M:%SZ)
$PUB -q 2 -t "safety/site1/alert" \
  -m "{\"deviceId\":\"RS6-Y\",\"siteId\":\"site1\",\"type\":\"scream\",\"alertId\":\"$aidY\",\"timestamp\":\"$tsY\"}"
sleep 5
Y=$(db_query "SELECT id FROM incidents WHERE alert_id='$aidY';")
[ -n "$Y" ] || { echo "SKIPPED: incident Y 생성 실패 (전제 미충족)"; exit 2; }
ystate0=$(db_query "SELECT COALESCE(resolved_at,'NULL') FROM incidents WHERE id=$Y;")
echo "incident Y id=$Y resolved_at(before restart)=$ystate0"
[ "$ystate0" = NULL ] || { echo "SKIPPED: incident Y 가 재시작 전 이미 해소됨 (전제 미충족)"; exit 2; }

echo "--- 3. restart hw-gateway → resubscribe → broker replays retained resolve (retain=1) ---"
SINCE=$(date -u +%Y-%m-%dT%H:%M:%SZ)
docker restart sentinel-hw-gateway >/dev/null
sleep 8

echo "=== hw-gateway logs after restart ==="
docker logs sentinel-hw-gateway --since "$SINCE" 2>&1 | grep -iE "RETAINED|ALERT-RESOLVED|subscrib" | tail -15

ystate1=$(db_query "SELECT COALESCE(resolved_at,'NULL') FROM incidents WHERE id=$Y;")
retlog=$(docker logs sentinel-hw-gateway --since "$SINCE" 2>&1 | grep -c "\[RETAINED\]")
echo "incident Y id=$Y resolved_at(after restart)=$ystate1  [RETAINED]log=$retlog"

# OK: Y 미해소 + [RETAINED] 로그. NOK: retained replay 가 Y 를 사람 게이트 없이 자동 해소.
if [ "$ystate1" = NULL ] && [ "$retlog" -ge 1 ]; then
  echo "OK: retained replay dropped — 사람 게이트 방어 유효"
  exit 0
fi
if [ "$ystate1" != NULL ]; then
  echo "NOK: retained replay 가 incident Y 를 자동 해소 ($ystate1) — 사람 게이트 우회"
  exit 1
fi
echo "NOK: incident Y 미해소이나 [RETAINED] 드롭 로그 부재 — 방어 경로 미확인"
exit 1
