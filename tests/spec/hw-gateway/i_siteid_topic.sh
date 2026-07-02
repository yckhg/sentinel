#!/usr/bin/env bash
# I. siteId 토픽 우선: payload siteY, 토픽 siteX → forward siteId=siteX
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

aid="T-I1-$(date +%s)"
$PUB -q 2 -t "safety/siteX/alert" \
  -m "{\"deviceId\":\"T-I1\",\"siteId\":\"siteY\",\"type\":\"scream\",\"alertId\":\"$aid\",\"timestamp\":\"$(date -u +%Y-%m-%dT%H:%M:%SZ)\"}"
sleep 4
sid=$(db_query "SELECT site_id FROM incidents WHERE alert_id='$aid';")
echo "site_id=$sid"
[ "$sid" = siteX ] && { echo OK; exit 0; } || { echo "NOK: $sid (기대 siteX)"; exit 1; }
