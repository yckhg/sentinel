#!/usr/bin/env bash
# R. 카메라 불변·검증 — streamKey cam-[0-9a-f]{8} 패턴, PUT 불변, rtsp+http 400, 사설 IP 400
# spec: docs/spec/web-backend.md — 검증 단언 (TDD)
# SKIP: mutating — 카메라 생성/수정 필요.
#       보조 관측(read-only): 기존 카메라 stream_key 패턴을 출력 (생성 경로 밖 유입 감지).
set -uo pipefail; . "$(dirname "$0")/../lib-web.sh"
keys=$(db_query "SELECT stream_key FROM cameras")
echo "INFO: 기존 stream_key = $(echo $keys | tr '\n' ' ')"
bad=$(echo "$keys" | grep -cEv '^cam-[0-9a-f]{8}$' || true)
[ "$bad" -gt 0 ] && echo "WARN: cam-{8hex} 패턴 밖 stream_key ${bad}건 — 서버 발급 경로 밖에서 유입되었을 가능성 (스펙 R의 '생성 응답 패턴' 자체는 미검증)"
require_mutating
T=$(get_token) || exit 1
out=$(bcurl -X POST -H "Authorization: Bearer $T" -H 'Content-Type: application/json' \
  -d '{"name":"spectdd-r","sourceType":"rtsp","sourceUrl":"rtsp://cam.example.com/s","enabled":false}' "$BACKEND/api/cameras")
id=$(echo "$out" | jq -r .id); key=$(echo "$out" | jq -r .streamKey)
echo "$key" | grep -qE '^cam-[0-9a-f]{8}$' || { bcurl -X DELETE -H "Authorization: Bearer $T" "$BACKEND/api/cameras/$id" >/dev/null; nok "발급 키 패턴 불일치: $key"; }
bcurl -X PUT -H "Authorization: Bearer $T" -H 'Content-Type: application/json' -d '{"streamKey":"cam-deadbeef"}' "$BACKEND/api/cameras/$id" >/dev/null
key2=$(bcurl -H "Authorization: Bearer $T" "$BACKEND/api/cameras" | jq -r ".[] | select(.id==$id) | .streamKey")
c1=$(bcode -X POST -H "Authorization: Bearer $T" -H 'Content-Type: application/json' -d '{"name":"x","sourceType":"rtsp","sourceUrl":"http://e.com/s","enabled":false}' "$BACKEND/api/cameras")
c2=$(bcode -X POST -H "Authorization: Bearer $T" -H 'Content-Type: application/json' -d '{"name":"x","sourceType":"rtsp","sourceUrl":"rtsp://192.168.1.10/s","enabled":false}' "$BACKEND/api/cameras")
bcurl -X DELETE -H "Authorization: Bearer $T" "$BACKEND/api/cameras/$id" >/dev/null  # cleanup
echo "immutable: $key == $key2 / http-scheme=$c1 private-ip=$c2"
[ "$key" = "$key2" ] && [ "$c1" = "400" ] && [ "$c2" = "400" ] && ok "패턴+불변+검증" || nok "불일치"
