#!/usr/bin/env bash
# 계약11-2. PUT site_url → 200, 이후 temp link url이 새 호스트 사용
# spec: docs/spec/interface-web-api.md 계약 11
# SKIP: mutating — 운영 설정(site_url) 변경 + 임시 링크 발급. 임시 링크 URL/초대 메일에 즉시 영향.
set -uo pipefail; . "$(dirname "$0")/../lib-web.sh"
require_mutating
T=$(get_token) || exit 1
orig=$(bcurl -H "Authorization: Bearer $T" "$BACKEND/api/settings" | jq -r '.[] | select(.key=="site_url") | .value')
bcurl -X PUT -H "Authorization: Bearer $T" -H 'Content-Type: application/json' -d '{"value":"https://x.example"}' "$BACKEND/api/settings/site_url" >/dev/null
body=$(bcurl -X POST -H "Authorization: Bearer $T" -H 'Content-Type: application/json' -d '{"label":"spec-tdd-siteurl"}' "$BACKEND/api/links/temp")
url=$(echo "$body" | jq -r .url); id=$(echo "$body" | jq -r .id)
# 원복 + cleanup
bcurl -X PUT -H "Authorization: Bearer $T" -H 'Content-Type: application/json' -d "{\"value\":\"$orig\"}" "$BACKEND/api/settings/site_url" >/dev/null
bcurl -X DELETE -H "Authorization: Bearer $T" "$BACKEND/api/links/$id" >/dev/null
echo "url=$url"
echo "$url" | grep -q '^https://x.example/view/' && ok "site_url 즉시 반영" || nok "url이 site_url 미반영"
