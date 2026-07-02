#!/usr/bin/env bash
# 단언 A. 헬스 — GET /healthz 가 200이고 body가 {"status":"ok","service":"recording"}
# 실행 정책: READ-ONLY — 프로덕션에서 실행 가능.
. "$(dirname "$0")/common.sh"

body=$(mktemp)
http_get "$REC/healthz" "$body"
[ "${STATUS:-}" = "200" ] && ok "HTTP 200" || nok "HTTP status=${STATUS:-none}"
b=$(cat "$body"); rm -f "$body"
if [ "$b" = '{"status":"ok","service":"recording"}' ]; then
  ok "body 정확 일치: $b"
else
  nok "body 불일치: $b"
fi
verdict A
