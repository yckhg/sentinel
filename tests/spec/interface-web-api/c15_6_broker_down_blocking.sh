#!/usr/bin/env bash
# 계약15-6. 연결 성립 후 mosquitto 중지 → POST /api/restart가 5초 내 미응답 (curl exit 28, 503 아님)
# spec: docs/spec/interface-web-api.md 계약 15 (hw-gateway.md O2 교차)
# SKIP: infra 조작 필요 — 프로덕션 mosquitto 중지 필요.
set -uo pipefail; . "$(dirname "$0")/../lib-web.sh"
require_mutating
skip "(infra 조작 필요): mosquitto 중지 시나리오는 설계자 입회 하 수행."
