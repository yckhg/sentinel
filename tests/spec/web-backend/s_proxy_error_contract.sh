#!/usr/bin/env bash
# S. proxy 오류 계약 — recording 정지 상태에서 GET /api/recordings/<key> → 502 + {"error":...}
# spec: docs/spec/web-backend.md — 검증 단언 (TDD)
# SKIP: infra 조작 필요 — 상시 녹화 서비스(recording) 중지는 녹화 공백을 만든다.
set -uo pipefail; . "$(dirname "$0")/../lib-web.sh"
require_mutating
skip "(infra 조작 필요): docker stop sentinel-recording 후 502+error 봉투 확인, 즉시 재기동. 설계자 입회 하 수행."
