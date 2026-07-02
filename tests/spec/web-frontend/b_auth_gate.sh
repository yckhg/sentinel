#!/usr/bin/env bash
# B. 인증 게이트 — 토큰 없음/만료 시 로그인 화면, 만료 토큰 localStorage 제거
# spec: docs/spec/web-frontend.md — 검증 단언 (TDD)
# SKIP: 브라우저 필요 — 수동/Playwright 별도. (렌더링·WS 주입·클릭 상호작용은 정적 curl로 판정 불가)
# Playwright 시나리오 개요: localStorage 조작 후 / 진입 → 로그인 폼 존재 + localStorage.getItem(token)==null 확인
set -uo pipefail
echo "SKIPPED (브라우저 필요 — 수동/Playwright 별도)"; exit 2
