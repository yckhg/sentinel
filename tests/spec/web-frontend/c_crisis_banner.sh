#!/usr/bin/env bash
# C. 위기 배너 즉시성 — crisis_alert WS 주입 → 전폭 배너 + 누적 + 탭 전환 유지
# spec: docs/spec/web-frontend.md — 검증 단언 (TDD)
# SKIP: 브라우저 필요 — 수동/Playwright 별도. (렌더링·WS 주입·클릭 상호작용은 정적 curl로 판정 불가)
# Playwright 시나리오 개요: WS mock 주입 2건 → 배너 2개(최신 위)·설명·KST·119버튼·닫기 확인, 네트워크 요청 무발생 확인
set -uo pipefail
echo "SKIPPED (브라우저 필요 — 수동/Playwright 별도)"; exit 2
