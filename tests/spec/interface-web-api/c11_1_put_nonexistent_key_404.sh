#!/usr/bin/env bash
# 계약11-1. PUT /api/settings/nonexistent_key → 404 (새 key 생성 불가)
# spec: docs/spec/interface-web-api.md 계약 11
# SKIP: mutating(PUT 정책) — 구현 오류 시 시스템 설정에 임의 key가 생성됨.
set -uo pipefail; . "$(dirname "$0")/../lib-web.sh"
require_mutating
T=$(get_token) || exit 1
code=$(bcode -X PUT -H "Authorization: Bearer $T" -H 'Content-Type: application/json' \
  -d '{"value":"x"}' "$BACKEND/api/settings/spectdd_nonexistent_key")
echo "code=$code"
[ "$code" = "404" ] && ok "404" || nok "기대 404, 관측 $code"
