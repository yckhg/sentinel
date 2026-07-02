#!/usr/bin/env bash
# I. 재시작 안전장치 — 매핑 없으면 안내만, 1단계 취소 시 요청 없음, 2단계 완료 시 POST 1건
# spec: docs/spec/web-frontend.md — 검증 단언 (TDD)
# SKIP: 브라우저 필요 — 수동/Playwright 별도. (렌더링·WS 주입·클릭 상호작용은 정적 curl로 판정 불가)
# Playwright 시나리오 개요: 카메라-장비 매핑 stub 조합별 클릭 흐름 → /api/equipment/restart 요청 수 검증 (mutating 주의: stub 백엔드로만)
set -uo pipefail
echo "SKIPPED (브라우저 필요 — 수동/Playwright 별도)"; exit 2
