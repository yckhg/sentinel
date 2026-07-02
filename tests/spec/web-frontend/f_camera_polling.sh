#!/usr/bin/env bash
# F. 목록 자동 갱신 — /api/cameras 30±5초 간격 반복
# spec: docs/spec/web-frontend.md — 검증 단언 (TDD)
# SKIP: 브라우저 필요 — 수동/Playwright 별도. (렌더링·WS 주입·클릭 상호작용은 정적 curl로 판정 불가)
# Playwright 시나리오 개요: CCTV 탭 고정 후 network 관찰 70초 → 요청 간격 측정
set -uo pipefail
echo "SKIPPED (브라우저 필요 — 수동/Playwright 별도)"; exit 2
