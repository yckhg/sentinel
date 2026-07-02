#!/usr/bin/env bash
# 계약14-5. user 클라이언트는 system_alarm 미수신
# spec: docs/spec/interface-web-api.md 계약 14
# SKIP: 검증 불가 — system_alarm을 발생시키는 수신 경로가 현재 없음(⚠️ 리뷰 항목 1) + user 토큰 fixture 부재.
set -uo pipefail; . "$(dirname "$0")/../lib-web.sh"
skip "(트리거 경로 부재 — ⚠️리뷰 1): system_alarm 발생 경로가 시스템에 없어 부정 단언을 유의미하게 관측할 수 없음"
