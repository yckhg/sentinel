#!/usr/bin/env bash
# I. 유한 지연 — 설정된 채널의 공급자 무응답 시 ≤12초 내 failed 종결(무한대기 없음).
# spec: docs/spec/notification-test-send.md — 단언 I (일반 · 무응답 공급자 픽스처 필요)
set -uo pipefail; . "$(dirname "$0")/../lib-web.sh"
skip "(부적절, no-config/no-gateway): 지연(무응답) 주입 공급자 픽스처 필요 — ≤12s failed 분기 공허"
