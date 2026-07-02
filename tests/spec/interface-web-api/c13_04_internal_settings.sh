#!/usr/bin/env bash
# 계약13-4. GET /internal/settings/{key} (무인증) → 200 {key,value} · 없는 key도 200 + value==""
# spec: docs/spec/interface-web-api.md 계약 13
set -uo pipefail; . "$(dirname "$0")/../lib-web.sh"
out1=$(bcurl_code "$BACKEND/internal/settings/site_url")
c1=$(echo "$out1" | tail -1); b1=$(echo "$out1" | head -n -1)
out2=$(bcurl_code "$BACKEND/internal/settings/spectdd_no_such_key")
c2=$(echo "$out2" | tail -1); b2=$(echo "$out2" | head -n -1)
echo "site_url: $c1 $b1"; echo "no_such_key: $c2 $b2"
[ "$c1" = "200" ] && echo "$b1" | jq -e '.key=="site_url" and has("value")' >/dev/null || nok "site_url 응답 불일치"
[ "$c2" = "200" ] && echo "$b2" | jq -e '.value == ""' >/dev/null || nok "없는 key가 200+빈 value 아님 (c=$c2)"
ok "internal settings 계약 일치"
