#!/usr/bin/env bash
# N. 마이그레이션 멱등 — _migrations 버전 중복 0행 + 기동 로그에 마이그레이션 오류 없음
# spec: docs/spec/web-backend.md — 검증 단언 (TDD)
# 참고: "컨테이너 2회 연속 기동" 원문은 프로덕션 재시작이 필요해 부분 검증(현재 DB/로그)으로 수행.
#       완전 검증은 설계자 승인 후 docker compose restart web-backend 2회로 수행.
set -uo pipefail; . "$(dirname "$0")/../lib-web.sh"
dups=$(db_query "SELECT version, COUNT(*) FROM _migrations GROUP BY version HAVING COUNT(*)>1")
total=$(db_query "SELECT COUNT(*) FROM _migrations")
errs=$(docker logs sentinel-web-backend 2>&1 | grep -ciE 'migration.*(fail|error)' || true)
echo "dup_rows='${dups}' total=$total migration_error_logs=$errs"
[ -z "$dups" ] && [ "$errs" = "0" ] && ok "중복 버전 0행 (${total}개 적용), 마이그레이션 오류 로그 없음 — 재기동 검증은 승인 대기" || nok "dups='$dups' errs=$errs"
