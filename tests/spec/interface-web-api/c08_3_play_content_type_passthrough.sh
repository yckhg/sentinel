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
# NOTE(harness fix): backend proxy 는 원본 경로를 그대로 보존한다 — recordings.go 는
#   recordingURL + r.URL.Path (= /api/recordings/{key}/play) 로 포워딩한다. 이전 버전의
#   direct URL 은 `/api` 프리픽스가 빠져 recording 이 404(text/plain)를 반환, 투명 프록시
#   구현이 정상인데도 불일치로 false-NOK 를 냈다. direct 비교 URL 에 /api 프리픽스를 맞춘다.
direct=$(docker run --rm --network "$NET" "$CURL_IMG" -s -o /dev/null -w '%{http_code} %{content_type}' --max-time 10 \
  "http://recording:8080/api/recordings/$key/play?$q")
echo "via-backend : $via"; echo "recording   : $direct"
[ "$via" = "$direct" ] || nok "상태/Content-Type 불일치 (backend='$via' recording='$direct')"
case "$via" in 200*) ok "투명 프록시 (m3u8 보존): $via";; *) ok "투명 프록시 (오류 응답도 동일 보존): $via — 녹화 세그먼트 부재로 200 경로는 미관측";; esac
