#!/usr/bin/env bash
# 계약14-4. POST /api/incidents → 접속 중 클라이언트 모두 crisis_alert 수신
# spec: docs/spec/interface-web-api.md 계약 14
# SKIP: mutating — 실제 incident 생성 필요 (c13_07과 동일 사유).
set -uo pipefail; . "$(dirname "$0")/../lib-web.sh"
require_mutating
skip "(mutating): c13_07_incident_create_ws_broadcast.sh 로 통합 실행 — 다중 role 클라이언트는 USER_TOKEN/temp 토큰 확보 시 확장"
