#!/usr/bin/env bash
# F. alertId dedup: 동일 alertId 2회 발행 → forward 각 1회만
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

# SINCE = 발행 직전 절대 타임스탬프. `--since 40s` 상대창은 순차 실행 시 직전 테스트의
# forward 로그를 계수에 섞어 dedup 판정을 오염(false-NOK)시킨다. 이 테스트 발행 이후의
# 로그만 계수하도록 시간창을 고정한다(incidents COUNT 는 alertId 로 이미 격리됨).
SINCE=$(date -u +%Y-%m-%dT%H:%M:%SZ)
ts=$(date -u +%Y-%m-%dT%H:%M:%SZ); aid="T-F1-$(date +%s)"
msg="{\"deviceId\":\"T-F1\",\"siteId\":\"site1\",\"type\":\"scream\",\"alertId\":\"$aid\",\"timestamp\":\"$ts\"}"
$PUB -q 2 -t "safety/site1/alert" -m "$msg"; sleep 3
$PUB -q 2 -t "safety/site1/alert" -m "$msg"; sleep 5
n=$(docker logs sentinel-hw-gateway --since "$SINCE" 2>&1 | grep -c "Notifier response")
w=$(docker logs sentinel-hw-gateway --since "$SINCE" 2>&1 | grep -c "Web-backend response")
inc=$(db_query "SELECT COUNT(*) FROM incidents WHERE alert_id='$aid';")
echo "notifier=$n web-backend=$w incidents=$inc"
[ "$n" = 1 ] && [ "$w" = 1 ] && [ "$inc" = 1 ] && { echo OK; exit 0; } || { echo "NOK: 중복 forward"; exit 1; }
