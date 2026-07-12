#!/usr/bin/env bash
# E. 채널 독립 — 이메일 테스트는 이메일 경로만(SMS 0건), SMS 테스트는 그 반대.
# spec: docs/spec/notification-test-send.md — 단언 E (핵심 · 설정 픽스처 필요)
# 두 채널 설정 + 채널별 발송-시도 관측 픽스처 없이는 공허 → SKIP.
set -uo pipefail; . "$(dirname "$0")/../lib-web.sh"
skip "(부적절, no-config/no-gateway): 이메일+SMS 설정 및 채널별 발송-시도 관측 픽스처 필요"
