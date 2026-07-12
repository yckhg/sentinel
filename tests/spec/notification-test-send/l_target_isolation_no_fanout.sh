#!/usr/bin/env bash
# L. 발송 대상 격리 — 지정 단일 대상 1건만, 등록 비상연락처(contactId) 팬아웃 0건.
# spec: docs/spec/notification-test-send.md — 단언 L (핵심 · 설정 픽스처 필요)
# 설정 + 등록 연락처(N≥1) + 발송-시도 관측 픽스처 없이는 공허 → SKIP.
set -uo pipefail; . "$(dirname "$0")/../lib-web.sh"
skip "(부적절, no-config/no-gateway): 설정 + 등록 연락처(N≥1) + 발송-시도 관측 픽스처 필요"
