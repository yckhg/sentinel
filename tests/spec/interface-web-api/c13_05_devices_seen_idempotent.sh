#!/usr/bin/env bash
# 계약13-5. POST /api/devices/seen 동일 바디 2회 → 둘 다 200, device 행 1개 (멱등)
# spec: docs/spec/interface-web-api.md 계약 13
# SKIP: mutating — devices 테이블에 테스트 장비 행 생성.
set -uo pipefail; . "$(dirname "$0")/../lib-web.sh"
require_mutating
c1=$(bcode -X POST -H 'Content-Type: application/json' -d '{"siteId":"spectdd","deviceId":"SPEC-IDEM-01"}' "$BACKEND/api/devices/seen")
c2=$(bcode -X POST -H 'Content-Type: application/json' -d '{"siteId":"spectdd","deviceId":"SPEC-IDEM-01"}' "$BACKEND/api/devices/seen")
n=$(db_query "SELECT COUNT(*) FROM devices WHERE site_id='spectdd' AND device_id='SPEC-IDEM-01'")
echo "codes=$c1,$c2 rows=$n"
[ "$c1" = "200" ] && [ "$c2" = "200" ] && [ "$n" = "1" ] && ok "멱등" || nok "codes=$c1,$c2 rows=$n"
