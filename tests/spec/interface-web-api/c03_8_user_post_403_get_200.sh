#!/usr/bin/env bash
# 계약3-8. user 토큰: POST → 403, GET → 200
# spec: docs/spec/interface-web-api.md 계약 3
# SKIP: fixture 부재(user 토큰) + POST는 mutating 게이트.
set -uo pipefail; . "$(dirname "$0")/../lib-web.sh"
[ -n "${USER_TOKEN:-}" ] || skip "(fixture 부재): USER_TOKEN env 필요"
g=$(bcode -H "Authorization: Bearer $USER_TOKEN" "$BACKEND/api/cameras")
require_mutating
p=$(bcode -X POST -H "Authorization: Bearer $USER_TOKEN" -H 'Content-Type: application/json' \
  -d '{"name":"x","sourceType":"rtsp","sourceUrl":"rtsp://cam.example.com/s","enabled":false}' "$BACKEND/api/cameras")
echo "GET=$g POST=$p"
[ "$g" = "200" ] && [ "$p" = "403" ] && ok "GET 200 / POST 403" || nok "GET=$g POST=$p"
