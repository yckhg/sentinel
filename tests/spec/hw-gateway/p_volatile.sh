#!/usr/bin/env bash
# P. 휘발성: hw-gateway 재시작 직후 equipment/status == []
# spec: docs/spec/hw-gateway.md — 검증 단언 (TDD)
set -uo pipefail
db_query() {
  docker run --rm -v sentinel_db-data:/data:ro alpine:3.19 \
    sh -c 'apk add -q sqlite >/dev/null && sqlite3 -readonly /data/sentinel.db "$1"' sh "$1"
}
PUB="docker exec sentinel-mosquitto mosquitto_pub -h localhost"
SUB="docker exec sentinel-mosquitto mosquitto_sub -h localhost"
gw_get() { docker exec sentinel-hw-gateway wget -q -O- "http://localhost:8080$1"; }
gw_post() { # $1=path $2=json — busybox wget POST, 응답 본문 출력 (에러 시 stderr에 HTTP 코드)
  docker exec sentinel-hw-gateway wget -S -q -O- --header "Content-Type: application/json" \
    --post-data "$2" "http://localhost:8080$1" 2>&1
}
# SKIP: mutating — 프로덕션 실행 금지 (실제 알림 발송/incident 생성/장비 명령/컨테이너 조작 유발).
if [ "${ALLOW_MUTATING:-0}" != "1" ]; then
  echo "SKIPPED (mutating — 설계자 승인 대기): ALLOW_MUTATING=1 로만 실행"
  exit 2
fi

# F4: 공유 라이브 스택에는 실시간 publisher(VOICE-01 등)가 상주하므로 재시작 직후에도
#     equipment/status 가 [] 이 아닐 수 있다(라이브 항목 재등록). 휘발성 계약은 "재시작 이전에
#     등록했던 항목의 생존 여부"로 판정한다 — 테스트 전용 고유 키 T-P1 을 재시작 전 등록하고,
#     재시작 직후 그 키가 부재함을 확인한다(라이브 publisher 신규 등록 항목은 허용).
DEV="T-P1"; SITE="site1"
$PUB -q 0 -t "safety/$SITE/heartbeat" \
  -m "{\"deviceId\":\"$DEV\",\"siteId\":\"$SITE\",\"status\":\"running\",\"alertState\":\"none\",\"timestamp\":\"$(date -u +%Y-%m-%dT%H:%M:%SZ)\"}"
sleep 2
# 사전 등록 확인(테스트 유의미성) — 최대 5s 폴링
reg=0
for _ in $(seq 1 5); do
  gw_get /api/equipment/status | grep -q "\"deviceId\":\"$DEV\"" && { reg=1; break; }
  sleep 1
done
[ "$reg" = 1 ] || { echo "NOK: 재시작 전 $DEV 등록 실패 (테스트 전제 불충족)"; exit 1; }

# ⚠️ 침습적: hw-gateway 재시작 — 재시작 중 MQTT 수신 공백 + dedup 캐시 초기화 + in-memory 장비 스토어 초기화.
docker restart sentinel-hw-gateway
sleep 5
out=$(gw_get /api/equipment/status)
echo "status=$out"
if echo "$out" | grep -q "\"deviceId\":\"$DEV\""; then
  echo "NOK: 재시작 후에도 $DEV 생존 — 휘발성 계약 위반"; exit 1
else
  echo "OK: 재시작 이전 등록 키 $DEV 부재 확인 (휘발성 성립; 라이브 재등록 항목은 무관)"; exit 0
fi
