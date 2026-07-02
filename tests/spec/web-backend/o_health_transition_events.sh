#!/usr/bin/env bash
# O. 건강 전이 이벤트 — 서비스 다운/복구 시 health_events 전이당 정확히 1행
# spec: docs/spec/web-backend.md — 검증 단언 (TDD)
# SKIP: infra 조작 필요 — 동료 서비스 중지/복구 필요.
#       보조 관측(read-only): 최근 컨테이너 기동 이후 연속 동일-status 이벤트 쌍을 센다.
set -uo pipefail; . "$(dirname "$0")/../lib-web.sh"
started=$(docker inspect -f '{{.State.StartedAt}}' sentinel-web-backend | cut -dT -f1,2 | tr T ' ' | cut -d. -f1)
dup=$(db_query "SELECT COUNT(*) FROM health_events a JOIN health_events b ON b.id=(SELECT MIN(id) FROM health_events x WHERE x.id>a.id AND x.entity_kind=a.entity_kind AND x.entity_id=a.entity_id) AND a.status=b.status AND a.detected_at > '$started' AND b.detected_at > '$started'")
echo "INFO: 현 기동($started) 이후 연속 동일-status 쌍 = $dup (0이어야 함 — 재시작 경계 제외)"
require_mutating
skip "(infra 조작 필요): 서비스 중지→임계 경과→복구 시나리오는 설계자 입회 하 수행."
