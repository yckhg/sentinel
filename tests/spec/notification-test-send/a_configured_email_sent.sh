#!/usr/bin/env bash
# A. 설정된 이메일 테스트 → outcome=sent + 이메일 시도 1건 + SMS 시도 0건.
# spec: docs/spec/notification-test-send.md — 단언 A (핵심 · 설정 픽스처 필요)
# mock SMTP + 발송-시도 관측 픽스처가 없으면 공허 → SKIP(부적절, no-config/no-gateway).
set -uo pipefail; . "$(dirname "$0")/../lib-web.sh"
skip "(부적절, no-config/no-gateway): mock SMTP + 발송-시도 관측 픽스처 필요 — 설정된 sent 분기 공허"
