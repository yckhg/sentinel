#!/usr/bin/env bash
# B. heartbeat → 장비 등록 (alive=true, lastHeartbeat=서버시각 ±5s)
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

now_epoch=$(date -u +%s)
$PUB -q 0 -t "safety/site1/heartbeat" \
  -m "{\"deviceId\":\"T-B1\",\"siteId\":\"site1\",\"status\":\"running\",\"alertState\":\"none\",\"timestamp\":\"$(date -u +%Y-%m-%dT%H:%M:%SZ)\"}"
sleep 2
entry=$(gw_get /api/equipment/status | grep -o "{\"deviceId\":\"T-B1\"[^}]*}")
echo "entry=$entry"
echo "$entry" | grep -q "\"siteId\":\"site1\",\"alive\":true" || { echo "NOK: alive=true 아님"; exit 1; }
echo "$entry" | grep -q "\"alertState\":\"none\"" || { echo "NOK: alertState"; exit 1; }
lh=$(echo "$entry" | grep -o "\"lastHeartbeat\":\"[^\"]*\"" | cut -d\" -f4)
lh_epoch=$(date -u -d "$lh" +%s)
diff=$(( lh_epoch - now_epoch )); [ $diff -lt 0 ] && diff=$(( -diff ))
echo "lastHeartbeat=$lh (Δ${diff}s)"
[ $diff -le 5 ] && { echo OK; exit 0; } || { echo "NOK: 서버시각 ±5s 초과"; exit 1; }
