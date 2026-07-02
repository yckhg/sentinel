#!/usr/bin/env bash
# A. 뷰포트 — index.html에 <meta name="viewport" content="width=device-width, ...">
# spec: docs/spec/web-frontend.md — 검증 단언 (TDD)
set -uo pipefail; . "$(dirname "$0")/../lib-web.sh"
html=$(bcurl "$FRONTEND/")
echo "$html" | grep -o '<meta name="viewport"[^>]*>' || true
echo "$html" | grep -q '<meta name="viewport" content="width=device-width' \
  && ok "viewport 메타 존재" || nok "viewport 메타 없음"
