#!/usr/bin/env bash
# L. 119 게이트 — 클릭 시 다이얼로그 먼저, Geolocation 거부에도 동작, 최종 클릭 시에만 tel:119
# spec: docs/spec/web-frontend.md — 검증 단언 (TDD)
# SKIP: 브라우저 필요 — 수동/Playwright 별도. (렌더링·WS 주입·클릭 상호작용은 정적 curl로 판정 불가)
# Playwright 시나리오 개요: geolocation deny 컨텍스트 → 다이얼로그 렌더·주소 fallback·tel: 네비게이션 발생 시점 확인
set -uo pipefail
echo "SKIPPED (브라우저 필요 — 수동/Playwright 별도)"; exit 2
