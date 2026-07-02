#!/usr/bin/env bash
# 계약15-5. mosquitto 정지 상태에서 hw-gateway (재)기동 직후 POST /api/restart → 503
# spec: docs/spec/interface-web-api.md 계약 15
# SKIP: infra 조작 필요 — 프로덕션 mosquitto 중지 + hw-gateway 재기동 (알람 수신 공백 발생).
set -uo pipefail; . "$(dirname "$0")/../lib-web.sh"
require_mutating
skip "(infra 조작 필요): mosquitto 정지 + hw-gateway 재기동 시나리오는 설계자 입회 하 수행."
