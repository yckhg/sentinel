#!/usr/bin/env bash
# 계약12-4. 서비스 중지 → threshold 후 unhealthy 전이 1행, 재시작 → healthy 전이 1행 (전이당 정확히 1행)
# spec: docs/spec/interface-web-api.md 계약 12
# SKIP: infra 조작 필요 — 프로덕션 서비스 컨테이너 중지/재시작 필요.
#       보조 관측(read-only): 동일 entity의 연속 동일-status 이벤트 쌍 수를 출력한다
#       (0이어야 이상적이나, 컨테이너 재시작 시 in-memory 스냅샷 휘발로 재기록될 수 있음 — 선언된 한계).
set -uo pipefail; . "$(dirname "$0")/../lib-web.sh"
dup=$(db_query "SELECT COUNT(*) FROM health_events a JOIN health_events b ON b.id=(SELECT MIN(id) FROM health_events x WHERE x.id>a.id AND x.entity_kind=a.entity_kind AND x.entity_id=a.entity_id) AND a.status=b.status")
echo "INFO: 연속 동일-status 이벤트 쌍(전 기간) = $dup"
require_mutating
skip "(infra 조작 필요): 임의 서비스 중지→임계 경과→재시작 시나리오는 설계자 입회 하 수행."
