#!/usr/bin/env bash
# D. WS 자기복구 — 절단 후 1s/2s/4s/8s 백오프 재접속, 이후 8s 초과 금지
# spec: docs/spec/web-frontend.md — 검증 단언 (TDD)
# SKIP: 브라우저 필요 — 수동/Playwright 별도. (렌더링·WS 주입·클릭 상호작용은 정적 curl로 판정 불가)
# Playwright 시나리오 개요: WS 서버 stub 강제 절단 → 재접속 타임스탬프 간격 측정 → 재접속 후 crisis_alert 주입 배너 확인
set -uo pipefail
echo "SKIPPED (브라우저 필요 — 수동/Playwright 별도)"; exit 2
