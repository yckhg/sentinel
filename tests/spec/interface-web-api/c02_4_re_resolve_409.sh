#!/usr/bin/env bash
# 계약2-4. 같은 incident에 resolve 재호출 → 409
# spec: docs/spec/interface-web-api.md 계약 2
# SKIP: mutating — resolve 상태 변경 전제 (c02_3 직후 실행 설계).
set -uo pipefail; . "$(dirname "$0")/../lib-web.sh"
require_mutating
T=$(get_token) || exit 1
id=$(bcurl -H "Authorization: Bearer $T" "$BACKEND/api/incidents?status=resolved&limit=1" | jq -r '.data[0].id // empty')
[ -n "$id" ] || skip "(fixture 부재): resolved incident 없음"
code=$(bcode -X PATCH -H "Authorization: Bearer $T" -H 'Content-Type: application/json' \
  -d '{"resolutionNotes":"dup"}' "$BACKEND/api/incidents/$id/resolve")
echo "code=$code"
[ "$code" = "409" ] && ok "재해결 409" || nok "기대 409, 관측 $code"
