#!/usr/bin/env bash
# P. admin 시드 — users에 username='admin' role=admin status=active 정확히 1행 (재기동 중복 없음)
# spec: docs/spec/web-backend.md — 검증 단언 (TDD)
set -uo pipefail; . "$(dirname "$0")/../lib-web.sh"
rows=$(db_query "SELECT role, status FROM users WHERE username='admin'")
n=$(db_query "SELECT COUNT(*) FROM users WHERE username='admin'")
echo "rows='$rows' count=$n"
[ "$rows" = "admin|active" ] && [ "$n" = "1" ] && ok "admin|active 1행 (여러 차례 기동 이력에도 중복 없음)" || nok "rows='$rows' n=$n"
