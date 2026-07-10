#!/usr/bin/env bash
# G. 필수 필드 누락 alert 무시: type 누락 → forward 0회 + 경고 로그
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

# SINCE = 발행 직전 절대 타임스탬프. `--since 20s` 상대창은 순차 실행 시 직전 테스트(E/F)의
# forward 로그를 "response" 계수에 섞어 forward!=0 오판(false-NOK)을 낸다. 이 테스트 발행
# 이후의 로그만 보도록 시간창을 고정한다.
SINCE=$(date -u +%Y-%m-%dT%H:%M:%SZ)
$PUB -q 2 -t "safety/site1/alert" \
  -m "{\"deviceId\":\"T-G1\",\"siteId\":\"site1\",\"timestamp\":\"$(date -u +%Y-%m-%dT%H:%M:%SZ)\"}"
sleep 4
gwlog=$(docker logs sentinel-hw-gateway --since "$SINCE" 2>&1)
fwd=$(echo "$gwlog" | grep -c "response")
echo "$gwlog" | grep -qi "missing required fields" || { echo "NOK: 경고 로그 없음"; exit 1; }
[ "$fwd" = 0 ] && { echo OK; exit 0; } || { echo "NOK: forward 발생"; exit 1; }
