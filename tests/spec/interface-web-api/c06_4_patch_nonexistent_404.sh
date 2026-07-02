#!/usr/bin/env bash
# 계약6-4. 존재하지 않는 id에 PATCH → 404
# spec: docs/spec/interface-web-api.md 계약 6
# SKIP: mutating(PATCH 정책) — read-only 원칙상 쓰기 동사 미실행 (대상 부재 확인은 DB로 가능).
set -uo pipefail; . "$(dirname "$0")/../lib-web.sh"
require_mutating
T=$(get_token) || exit 1
missing=99999999
n=$(db_query "SELECT COUNT(*) FROM devices WHERE id=$missing")
[ "$n" = "0" ] || nok "전제 실패: id=$missing 존재"
code=$(bcode -X PATCH -H "Authorization: Bearer $T" -H 'Content-Type: application/json' \
  -d '{"alias":"x"}' "$BACKEND/api/devices/$missing")
echo "code=$code"
[ "$code" = "404" ] && ok "404" || nok "기대 404, 관측 $code"
