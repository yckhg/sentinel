#!/usr/bin/env bash
# G. 타임아웃 UX — 10초 이상 지연 시 '서버 응답 시간이 초과되었습니다...' 표시
# spec: docs/spec/web-frontend.md — 검증 단언 (TDD)
# SKIP: 브라우저 필요 — 수동/Playwright 별도. (렌더링·WS 주입·클릭 상호작용은 정적 curl로 판정 불가)
# Playwright 시나리오 개요: /api/cameras 응답 12초 지연 stub → 안내 문구 렌더 확인
set -uo pipefail
echo "SKIPPED (브라우저 필요 — 수동/Playwright 별도)"; exit 2
