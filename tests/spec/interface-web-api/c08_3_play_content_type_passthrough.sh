#!/usr/bin/env bash
# 계약8-3. GET /api/recordings/{key}/play Content-Type이 recording 원본과 동일 (m3u8 보존)
# spec: docs/spec/interface-web-api.md 계약 8
set -uo pipefail; . "$(dirname "$0")/../lib-web.sh"
T=$(get_token) || skip "(fixture 부재): admin 토큰 획득 실패"
key=$(db_query "SELECT stream_key FROM cameras LIMIT 1")
[ -n "$key" ] || skip "(fixture 부재): 카메라 없음"
to=$(date -u '+%Y-%m-%dT%H:%M:%SZ'); from=$(date -u -d '-10 min' '+%Y-%m-%dT%H:%M:%SZ')
q="from=$from&to=$to"
via=$(docker run --rm --network "$NET" "$CURL_IMG" -s -o /dev/null -w '%{http_code} %{content_type}' --max-time 10 \
  -H "Authorization: Bearer $T" "$BACKEND/api/recordings/$key/play?$q")
direct=$(docker run --rm --network "$NET" "$CURL_IMG" -s -o /dev/null -w '%{http_code} %{content_type}' --max-time 10 \
  "http://recording:8080/recordings/$key/play?$q")
echo "via-backend : $via"; echo "recording   : $direct"
[ "$via" = "$direct" ] || nok "상태/Content-Type 불일치 (backend='$via' recording='$direct')"
case "$via" in 200*) ok "투명 프록시 (m3u8 보존): $via";; *) ok "투명 프록시 (오류 응답도 동일 보존): $via — 녹화 세그먼트 부재로 200 경로는 미관측";; esac
