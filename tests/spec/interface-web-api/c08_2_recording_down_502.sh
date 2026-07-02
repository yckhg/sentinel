#!/usr/bin/env bash
# 계약8-2. recording 컨테이너 중지 후 GET /api/archives → 502
# spec: docs/spec/interface-web-api.md 계약 8
# SKIP: infra 조작 필요 — 프로덕션 recording(상시 녹화) 중지는 녹화 공백을 만든다.
set -uo pipefail; . "$(dirname "$0")/../lib-web.sh"
require_mutating
skip "(infra 조작 필요): docker stop sentinel-recording 후 GET /api/archives → 502 확인, 즉시 재기동. 설계자 입회 하 수행."
