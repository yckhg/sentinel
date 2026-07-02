#!/usr/bin/env bash
# H. 뷰어 링크 — 유효 토큰: 라이브 그리드만 · 무효 토큰: 만료 안내 + 카메라 요청 없음
# spec: docs/spec/web-frontend.md — 검증 단언 (TDD)
# SKIP: 브라우저 필요 — 수동/Playwright 별도. (렌더링·WS 주입·클릭 상호작용은 정적 curl로 판정 불가)
# Playwright 시나리오 개요: /view/{token} 진입 (verify 200/401 stub) → 탭바 DOM 부재·안내 문구·network 무요청 확인
set -uo pipefail
echo "SKIPPED (브라우저 필요 — 수동/Playwright 별도)"; exit 2
