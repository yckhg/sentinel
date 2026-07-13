#!/usr/bin/env bash
# H. 설정됐어도 전송 실패는 failed로 보고(sent/ not_configured 아님).
# spec: docs/spec/notification-test-send.md — 단언 H (일반 · 실패 공급자 픽스처 필요)
set -uo pipefail; . "$(dirname "$0")/../lib-web.sh"
skip "(부적절, no-config/no-gateway): 실패 주입 공급자 픽스처 필요 — 설정됨→failed 분기 공허"
