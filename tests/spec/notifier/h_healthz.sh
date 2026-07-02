#!/usr/bin/env bash
# H. 헬스체크: GET /healthz → 200 + status:ok (read-only)
# spec: docs/spec/notifier.md — 검증 단언 (TDD)
set -uo pipefail
db_query() {
  docker run --rm -v sentinel_db-data:/data:ro alpine:3.19 \
    sh -c 'apk add -q sqlite >/dev/null && sqlite3 -readonly /data/sentinel.db "$1"' sh "$1"
}
# Docker 내부망에서 notifier로 직접 주입 (curl은 일회성 컨테이너에 설치 — 호스트 미오염)
ncurl() {
  docker run --rm --network sentinel_sentinel-net alpine:3.19 sh -c \
    "apk add -q curl >/dev/null && curl $*"
}

out=$(docker exec sentinel-notifier wget -S -q -O- http://localhost:8080/healthz 2>&1)
echo "$out"
echo "$out" | grep -q "HTTP/1.1 200" && echo "$out" | grep -q "\"status\":\"ok\"" \
  && { echo OK; exit 0; } || { echo NOK; exit 1; }
