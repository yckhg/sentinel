#!/usr/bin/env bash
# M. candidate는 참고용: devices/seen만, incidents/notify 호출 없음
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

$PUB -q 0 -t "safety/site1/event/candidate" \
  -m "{\"deviceId\":\"T-M1\",\"siteId\":\"site1\",\"class\":\"save_me\",\"confidence\":0.6,\"threshold\":0.8}"
sleep 4
inc=$(db_query "SELECT COUNT(*) FROM incidents WHERE device_id='T-M1';")
dev=$(db_query "SELECT COUNT(*) FROM devices WHERE device_id='T-M1';")
notif=$(docker logs sentinel-notifier --since 20s 2>&1 | grep -c "device=T-M1")
echo "devices=$dev incidents=$inc notifier호출=$notif"
[ "$dev" -ge 1 ] && [ "$inc" = 0 ] && [ "$notif" = 0 ] && { echo OK; exit 0; } || { echo NOK; exit 1; }
