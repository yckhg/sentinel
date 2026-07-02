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

# 전제: b_heartbeat_register.sh 직후. HEARTBEAT_TIMEOUT_SEC=30 기준 40초 대기.
echo "40초 대기..."; sleep 40
entry=$(gw_get /api/equipment/status | grep -o "{\"deviceId\":\"T-B1\"[^}]*}")
echo "entry=$entry"
[ -n "$entry" ] || { echo "NOK: 목록에서 제거됨"; exit 1; }
echo "$entry" | grep -q "\"alive\":false" && { echo OK; exit 0; } || { echo "NOK: alive=true 유지"; exit 1; }
