#!/usr/bin/env bash
# 계약13-3. GET /internal/cameras (무인증) → 200 배열, streamKey/sourceType/sourceUrl 포함, hlsUrl/status 항상 빈 값
# spec: docs/spec/interface-web-api.md 계약 13
set -uo pipefail; . "$(dirname "$0")/../lib-web.sh"
out=$(bcurl_code "$BACKEND/internal/cameras")
code=$(echo "$out" | tail -1); body=$(echo "$out" | head -n -1)
echo "code=$code"; echo "$body" | jq -c '[.[] | {id, streamKey, sourceType, hlsUrl, status}]' 2>/dev/null
[ "$code" = "200" ] || nok "기대 200, 관측 $code"
echo "$body" | jq -e 'type=="array" and all(.[]; has("streamKey") and has("sourceType") and has("sourceUrl"))' >/dev/null || nok "필수 필드 누락"
echo "$body" | jq -e 'all(.[]; (.hlsUrl // "") == "" and (.status // "") == "")' >/dev/null \
  && ok "DB 원본만 반환 (hlsUrl/status 빈 값)" || nok "streaming 병합 값 존재 (계약과 다름)"
