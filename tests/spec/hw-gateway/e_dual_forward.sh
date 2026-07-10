#!/usr/bin/env bash
# E. alert 이중 forward: notifier POST /api/notify + web-backend POST /api/incidents 각 1회
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

# SINCE = 발행 직전 절대 타임스탬프. `--since 30s` 상대창은 순차 실행 시 직전 테스트의
# forward 로그가 창에 섞여 계수를 오염(false-NOK)시킨다. FORWARD 로그는 status만 담아
# deviceId 로 필터할 수 없으므로, 이 테스트 발행 이후의 로그만 보도록 시간창을 고정한다.
SINCE=$(date -u +%Y-%m-%dT%H:%M:%SZ)
ts=$(date -u +%Y-%m-%dT%H:%M:%SZ); aid="T-E1-$(date +%s)"; mark=$(date -u +%H:%M)
$PUB -q 2 -t "safety/site1/alert" \
  -m "{\"deviceId\":\"T-E1\",\"siteId\":\"site1\",\"type\":\"scream\",\"alertId\":\"$aid\",\"timestamp\":\"$ts\",\"description\":\"spec E\"}"
sleep 5
gwlog=$(docker logs sentinel-hw-gateway --since "$SINCE" 2>&1)
echo "$gwlog" | grep -c "Notifier response" | grep -q "^1$" || { echo "NOK: notifier forward != 1"; exit 1; }
echo "$gwlog" | grep -c "Web-backend response" | grep -q "^1$" || { echo "NOK: web-backend forward != 1"; exit 1; }
docker logs sentinel-notifier --since "$SINCE" 2>&1 | grep -q "Received alert: site=site1 device=T-E1" || { echo "NOK: notifier 미수신"; exit 1; }
oa=$(db_query "SELECT occurred_at FROM incidents WHERE alert_id='$aid';")
echo "occurred_at=$oa (기대 $ts)"
[ -n "$oa" ] && { echo OK; exit 0; } || { echo "NOK: incident 없음"; exit 1; }
