#!/usr/bin/env bash
# K. 웹 해소 발행 + echo 무시: 200 + 1건 수신 + resolve-from-sensor 미호출
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

out=/tmp/k_sub.$$
timeout 12 $SUB -v -q 1 -W 10 -t "safety/site1/alert/resolved" > "$out" &
subpid=$!; sleep 2
resp=$(gw_post /api/alert/resolved "{\"incidentId\":1,\"siteId\":\"site1\",\"resolvedBy\":{\"kind\":\"web\",\"id\":\"admin\",\"label\":\"관리자\"}}")
echo "resp=$resp"
echo "$resp" | grep -q "HTTP/1.1 200" || { echo "NOK: 200 아님"; exit 1; }
wait $subpid; msg=$(grep -c "^safety/site1/alert/resolved" "$out"); rm -f "$out"
sleep 3
gwlog=$(docker logs sentinel-hw-gateway --since 20s 2>&1)
echo "$gwlog" | grep -q "Ignoring echo" || { echo "NOK: echo 무시 로그 없음"; exit 1; }
echo "$gwlog" | grep -q "resolve-from-sensor" && { echo "NOK: resolve-from-sensor 호출됨"; exit 1; }
[ "$msg" -ge 1 ] && { echo OK; exit 0; } || { echo "NOK: 구독 미수신"; exit 1; }
