#!/usr/bin/env bash
# SKIP: mutating — 설정 파일 제거/훼손 + 컨테이너 재기동이 필요 (설계자 승인 대기).
#   참고(read-only 관측): 현재 프로덕션은 빈 배열([]) 설정으로 기동된 상태에서
#   healthz 200 + status [] 를 반환 — "카메라 0대 관용 기동"의 인접 증거.
# cctv-adapter §단언 E (부트 설정 관용성): 설정 부재/깨진 JSON으로 기동해도
#   프로세스 생존 + /healthz 200 + /api/cameras/status == [].
set -u

if [ "${SPEC_TDD_ALLOW_MUTATING:-0}" != "1" ]; then
  echo "SKIPPED: requires config removal/corruption + container restart."
  echo "--- read-only adjacent evidence (empty-array config) ---"
  docker exec sentinel-cctv-adapter sh -c 'cat ${CAMERAS_CONFIG_PATH:-/config/cameras.json}'
  docker exec sentinel-cctv-adapter wget -qO- http://localhost:8080/healthz; echo
  docker exec sentinel-cctv-adapter wget -qO- http://localhost:8080/api/cameras/status; echo
  exit 2
fi

# 승인 시 절차: 깨진 설정을 마운트한 임시 컨테이너로 검증 (프로덕션 컨테이너 비접촉)
TMPD=$(mktemp -d); echo '{broken' > "$TMPD/cameras.json"
docker run -d --rm --name spec-cctv-e --network sentinel_sentinel-net \
  -v "$TMPD/cameras.json:/config/cameras.json:ro" \
  "$(docker inspect sentinel-cctv-adapter --format '{{.Config.Image}}')" >/dev/null
trap 'docker rm -f spec-cctv-e >/dev/null 2>&1; rm -rf "$TMPD"' EXIT
sleep 3
H=$(docker exec spec-cctv-e wget -qO- http://localhost:8080/healthz)
S=$(docker exec spec-cctv-e wget -qO- http://localhost:8080/api/cameras/status)
echo "healthz: $H"; echo "status: $S"
printf '%s' "$H" | jq -e '.status=="ok"' >/dev/null && [ "$S" = "[]" ] \
  && { echo "OK"; exit 0; }
echo "NOK"
exit 1
