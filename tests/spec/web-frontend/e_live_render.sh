#!/usr/bin/env bash
# E. 라이브 렌더 — 카메라 N개 타일, connected는 HLS 로드, disconnected는 '연결 끊김' + 요청 없음
# spec: docs/spec/web-frontend.md — 검증 단언 (TDD)
# SKIP: 브라우저 필요 — 수동/Playwright 별도. (렌더링·WS 주입·클릭 상호작용은 정적 curl로 판정 불가)
# Playwright 시나리오 개요: /api/cameras stub 주입 → 타일 수·HLS 요청 발생 여부·연결 끊김 문구 확인
set -uo pipefail
echo "SKIPPED (브라우저 필요 — 수동/Playwright 별도)"; exit 2
