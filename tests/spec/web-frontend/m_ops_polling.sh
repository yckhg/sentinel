#!/usr/bin/env bash
# M. 운영 폴링 — /api/health 15±3초, /api/devices 10±3초 반복
# spec: docs/spec/web-frontend.md — 검증 단언 (TDD)
# SKIP: 브라우저 필요 — 수동/Playwright 별도. (렌더링·WS 주입·클릭 상호작용은 정적 curl로 판정 불가)
# Playwright 시나리오 개요: 관리 탭 고정 40초 network 관찰 → 간격 측정
set -uo pipefail
echo "SKIPPED (브라우저 필요 — 수동/Playwright 별도)"; exit 2
