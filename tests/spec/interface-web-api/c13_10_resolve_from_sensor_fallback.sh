#!/usr/bin/env bash
# 계약13-10. resolve-from-sensor 0/0 + siteId → 최신 미해결 incident resolved, resolvedByKind=="sensor_button"
# spec: docs/spec/interface-web-api.md 계약 13
# SKIP: mutating — 실제 미해결 incident를 해소함.
set -uo pipefail; . "$(dirname "$0")/../lib-web.sh"
require_mutating
# 자기 fixture(spectdd site)로만 수행
bcurl -X POST -H 'Content-Type: application/json' -d '{"siteId":"spectdd","description":"t1","isTest":true}' "$BACKEND/api/incidents" >/dev/null
out=$(bcurl -X POST -H 'Content-Type: application/json' \
  -d '{"incidentId":0,"siteId":"spectdd","resolvedBy":{"kind":"sensor_button","id":"SPEC-BTN","label":"spec"}}' \
  "$BACKEND/api/incidents/0/resolve-from-sensor")
echo "$out" | jq -c .
echo "$out" | jq -e '.status=="resolved" and .resolvedByKind=="sensor_button"' >/dev/null && ok "fallback resolve" || nok "응답 불일치"
