#!/usr/bin/env bash
# C. dead 판정: 타임아웃+10s 후 alive=false, 목록 유지
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

# F5: C 전용 deviceId(T-C1) — B(T-B1)/D 와 키 공유 금지(공유 키는 dead 타이머 리셋 → C 교차오염).
#     C는 자체적으로 T-C1 을 등록한 뒤 재수신을 중단해 dead 판정을 격리 관측한다.
# 격리: R의 소TTL(EQUIPMENT_EVICT_TTL_SEC≈10) 설정 인스턴스와 같은 인스턴스에서 돌리지 않는다 —
#       기본 설정(TTL 86400 ≫ HEARTBEAT_TIMEOUT_SEC 30)에서 실행해 TTL 제거와 분리한다.
DEV="T-C1"; SITE="site1"
$PUB -q 0 -t "safety/$SITE/heartbeat" \
  -m "{\"deviceId\":\"$DEV\",\"siteId\":\"$SITE\",\"status\":\"running\",\"alertState\":\"none\",\"timestamp\":\"$(date -u +%Y-%m-%dT%H:%M:%SZ)\"}"
sleep 2
gw_get /api/equipment/status | grep -q "\"deviceId\":\"$DEV\"" || { echo "NOK: $DEV 등록 실패 (전제 불충족)"; exit 1; }

# HEARTBEAT_TIMEOUT_SEC=30 기준 40초(=timeout+10) 재수신 중단 대기.
echo "40초 대기 (dead 판정)..."; sleep 40
entry=$(gw_get /api/equipment/status | grep -o "{\"deviceId\":\"$DEV\"[^}]*}")
echo "entry=$entry"
[ -n "$entry" ] || { echo "NOK: 목록에서 제거됨 (TTL 미도달인데 사라짐)"; exit 1; }
echo "$entry" | grep -q "\"alive\":false" && { echo OK; exit 0; } || { echo "NOK: alive=true 유지 (dead 미마킹)"; exit 1; }
