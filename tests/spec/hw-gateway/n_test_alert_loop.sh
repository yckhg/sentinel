#!/usr/bin/env bash
# N. test-alert 순환: POST /api/test-alert → safety/test/alert 발행 → isTest incident
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

before=$(db_query "SELECT COUNT(*) FROM incidents WHERE is_test=1;")
out=/tmp/n_sub.$$
timeout 12 $SUB -v -q 1 -W 10 -t "safety/test/alert" > "$out" &
subpid=$!; sleep 2
resp=$(gw_post /api/test-alert "{}")
echo "resp=$resp"
echo "$resp" | grep -q "HTTP/1.1 200" || { echo "NOK: 200 아님"; exit 1; }
wait $subpid; msg=$(cat "$out"); rm -f "$out"; echo "수신: $msg"
echo "$msg" | grep -q "\"test\":true" && echo "$msg" | grep -q "\"deviceId\":\"TEST-DEVICE\"" || { echo "NOK: 발행 페이로드"; exit 1; }
sleep 3
after=$(db_query "SELECT COUNT(*) FROM incidents WHERE is_test=1;")
echo "is_test incidents: $before -> $after"
[ "$after" -gt "$before" ] && { echo OK; exit 0; } || { echo "NOK: isTest incident 미생성"; exit 1; }
