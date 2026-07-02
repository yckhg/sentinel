#!/usr/bin/env bash
# 계약4-2. {"managerPhone":""} PUT → 200, 기존 phone 유지
# spec: docs/spec/interface-web-api.md 계약 4
# SKIP: mutating — PUT /api/sites.
set -uo pipefail; . "$(dirname "$0")/../lib-web.sh"
require_mutating
T=$(get_token) || exit 1
before=$(bcurl -H "Authorization: Bearer $T" "$BACKEND/api/sites" | jq -r '.[0] // empty')
[ -n "$before" ] || skip "(fixture 부재): sites 비어 있음"
id=$(echo "$before" | jq -r .id); phone=$(echo "$before" | jq -r .managerPhone)
out=$(bcurl -X PUT -H "Authorization: Bearer $T" -H 'Content-Type: application/json' \
  -d '{"managerPhone":""}' "$BACKEND/api/sites/$id")
after=$(echo "$out" | jq -r .managerPhone)
echo "before=$phone after=$after"
[ "$after" = "$phone" ] && ok "빈 값 무시, phone 유지" || nok "phone이 빈 값으로 덮임"
