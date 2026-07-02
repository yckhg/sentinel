#!/usr/bin/env bash
# J. 사고 이력 계약 — 총 57건 표기, 20카드, 더 보기 → page=2, sensor_button 해제 표시
# spec: docs/spec/web-frontend.md — 검증 단언 (TDD)
# SKIP: 브라우저 필요 — 수동/Playwright 별도. (렌더링·WS 주입·클릭 상호작용은 정적 curl로 판정 불가)
# Playwright 시나리오 개요: /api/incidents stub 주입 → 카드 수·총계 문구·page=2 요청·센서 버튼 문구 확인
set -uo pipefail
echo "SKIPPED (브라우저 필요 — 수동/Playwright 별도)"; exit 2
