#!/usr/bin/env bash
# 계약7-2. hw-gateway 다운 상태에서 등록 device restart → 502
# spec: docs/spec/interface-web-api.md 계약 7
# SKIP: mutating/infra — 프로덕션 hw-gateway 컨테이너 중지 + restart 명령 발사 필요.
set -uo pipefail; . "$(dirname "$0")/../lib-web.sh"
require_mutating
skip "(infra 조작 필요): docker stop sentinel-hw-gateway 후 등록 장비로 restart → 502 확인, 즉시 재기동. 설계자 입회 하 수행."
