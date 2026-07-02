#!/usr/bin/env bash
# H. 타임스탬프 위생: 1970 timestamp → occurred_at이 서버 현재시각 ±10s
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

aid="T-H1-$(date +%s)"; now_epoch=$(date -u +%s)
$PUB -q 2 -t "safety/site1/alert" \
  -m "{\"deviceId\":\"T-H1\",\"siteId\":\"site1\",\"type\":\"scream\",\"alertId\":\"$aid\",\"timestamp\":\"1970-01-01T00:00:00Z\"}"
sleep 4
oa=$(db_query "SELECT occurred_at FROM incidents WHERE alert_id='$aid';")
[ -n "$oa" ] || { echo "NOK: incident 없음"; exit 1; }
oa_epoch=$(date -u -d "$oa" +%s); diff=$(( oa_epoch - now_epoch )); [ $diff -lt 0 ] && diff=$(( -diff ))
echo "occurred_at=$oa (Δ${diff}s)"
[ $diff -le 10 ] && { echo OK; exit 0; } || { echo "NOK: 서버 시각 대체 안 됨"; exit 1; }
