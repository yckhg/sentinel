#!/usr/bin/env bash
# K. 장비 자동 영속·부활 — soft-delete 후 devices/seen → deleted_at NULL, last_seen 갱신
# spec: docs/spec/web-backend.md — 검증 단언 (TDD)
# SKIP: mutating — devices 변경.
set -uo pipefail; . "$(dirname "$0")/../lib-web.sh"
require_mutating
T=$(get_token) || exit 1
bcurl -X POST -H 'Content-Type: application/json' -d '{"siteId":"spectdd","deviceId":"SPEC-K-01"}' "$BACKEND/api/devices/seen" >/dev/null
id=$(db_query "SELECT id FROM devices WHERE site_id='spectdd' AND device_id='SPEC-K-01'")
bcurl -X DELETE -H "Authorization: Bearer $T" "$BACKEND/api/devices/$id" >/dev/null
ls1=$(db_query "SELECT last_seen FROM devices WHERE id=$id")
sleep 1.5
bcurl -X POST -H 'Content-Type: application/json' -d '{"siteId":"spectdd","deviceId":"SPEC-K-01"}' "$BACKEND/api/devices/seen" >/dev/null
row=$(db_query "SELECT deleted_at, last_seen FROM devices WHERE id=$id")
del=${row%%|*}; ls2=${row##*|}
echo "deleted_at='$del' last_seen: $ls1 -> $ls2"
[ -z "$del" ] && [ "$ls1" != "$ls2" ] && ok "부활 + last_seen 갱신" || nok "불일치"
