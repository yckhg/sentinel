#!/usr/bin/env bash
# streaming §단언 G (휘발성) / interface-streaming §계약 4 — 원천(source-of-truth) 가드.
#
# G_volatility.sh 는 *실행 중* 컨테이너의 tmpfs(/proc/mounts + restart-wipe)를 검증하지만,
#   docker-compose.yml 소스가 `streaming` 서비스에 `tmpfs: /tmp/hls` 를 선언하는지는 아무도
#   단언하지 않는다 — compose 에서 그 줄이 삭제되어도 다음 재생성 전까지는 통과한다(잠복 회귀).
# 본 게이트는 그 원천을 단언한다: repo 의 docker-compose.yml 의 `streaming` 서비스 블록이
#   /tmp/hls 에 대한 tmpfs 마운트를 선언하는가.
#
# READ-ONLY: 컨테이너를 재시작/변경하지 않는다 — SPEC_TDD_ALLOW_MUTATING 불필요.
set -u

# 워크트리 병합 후에도 견고하도록 repo 루트 기준으로 compose 를 찾는다.
ROOT=$(git rev-parse --show-toplevel 2>/dev/null)
if [ -z "${ROOT}" ]; then
  echo "NOK: git repo 루트를 찾을 수 없음 (git rev-parse --show-toplevel 실패)"
  exit 1
fi
COMPOSE="${ROOT}/docker-compose.yml"
if [ ! -f "${COMPOSE}" ]; then
  echo "NOK: compose 파일 없음: ${COMPOSE}"
  exit 1
fi
echo "compose: ${COMPOSE}"

# `streaming:` 서비스 블록만 추출: 서비스 헤더('  streaming:')부터 다음 최상위 서비스 헤더
#   ('  <name>:') 직전까지. 그 블록 안에서 tmpfs: 하위에 /tmp/hls 항목이 선언돼야 한다.
BLOCK=$(awk '
  /^  streaming:[[:space:]]*$/ { inblk=1; next }
  inblk && /^  [A-Za-z0-9_-]+:[[:space:]]*$/ { inblk=0 }
  inblk { print }
' "${COMPOSE}")

if [ -z "${BLOCK}" ]; then
  echo "NOK: docker-compose.yml 에서 streaming 서비스 블록을 찾을 수 없음"
  exit 1
fi

# 블록 내 tmpfs: 키가 있고, 그 아래(또는 인라인)에 /tmp/hls 항목이 존재하는지.
if ! printf '%s\n' "${BLOCK}" | grep -qE '^[[:space:]]*tmpfs:'; then
  echo "NOK: streaming 서비스 블록에 tmpfs: 선언이 없음 — 휘발성 원천 소실(회귀)"
  printf '%s\n' "${BLOCK}"
  exit 1
fi

# tmpfs: 다음의 리스트 항목/인라인에서 /tmp/hls 을 찾는다.
if printf '%s\n' "${BLOCK}" | grep -Eq '^[[:space:]]*(-[[:space:]]*)?/tmp/hls([[:space:]:].*)?$'; then
  echo "OK: docker-compose.yml streaming 서비스가 tmpfs: /tmp/hls 를 선언함 (원천 휘발성 가드 확인)"
  exit 0
fi

echo "NOK: streaming 서비스에 tmpfs: 는 있으나 /tmp/hls 항목이 없음 — HLS 출력 휘발성 미보장"
printf '%s\n' "${BLOCK}"
exit 1
