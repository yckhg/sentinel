#!/usr/bin/env bash
# A. 헬스체크: GET /healthz → 200 + "status":"ok" (read-only)
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

out=$(docker exec sentinel-hw-gateway wget -S -q -O- http://localhost:8080/healthz 2>&1)
echo "$out"
echo "$out" | grep -q "HTTP/1.1 200" && echo "$out" | grep -q "\"status\":\"ok\"" \
  && { echo OK; exit 0; } || { echo NOK; exit 1; }
