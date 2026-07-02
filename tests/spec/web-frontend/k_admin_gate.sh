#!/usr/bin/env bash
# K. admin 게이트 — user 토큰: 확인/조치 버튼 미렌더 · admin: 렌더 + 빈 조치 내용이면 미전송
# spec: docs/spec/web-frontend.md — 검증 단언 (TDD)
# SKIP: 브라우저 필요 — 수동/Playwright 별도. (렌더링·WS 주입·클릭 상호작용은 정적 curl로 판정 불가)
# Playwright 시나리오 개요: role 클레임별 토큰 주입 → 버튼 DOM 유무·빈 notes 제출 시 network 무요청 확인
set -uo pipefail
echo "SKIPPED (브라우저 필요 — 수동/Playwright 별도)"; exit 2
