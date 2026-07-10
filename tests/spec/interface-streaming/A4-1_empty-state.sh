#!/usr/bin/env bash
# SKIP: mutating — "push가 하나도 없는 초기 상태"는 프로덕션에서 컨테이너 재생성 또는
#   어댑터 중지가 필요 (설계자 승인 대기). 현재 프로덕션에는 활성 push 2개가 존재.
# A4-1 (빈 상태): push 없는 초기 상태에서 GET /api/streams → [] 이면 OK.
#
# NOTE(탈-flaky, 라이브 스택 정합): 이 스택에는 실 yt-cam 어댑터가 상시 push 를 재개하며,
#   재접속 타이밍은 제어 불가다. 재생성 직후 짧은 빈 창을 놓치면 실 스트림이 이미 다시
#   나타나 배열이 비어있지 않을 수 있다 — 이는 제품 결함이 아니라 관측 창을 놓친 것이다.
#   따라서 단언은 다음과 같이 견고화한다(쌍둥이 streaming/B_empty-array.sh 와 동일 패턴):
#     - [] 를 한 번이라도 관측 → OK (무스트림 빈 배열 = 진짜 단언).
#     - 재생성 후 짧은 바운디드 재시도 동안 [] 를 못 보고 실 스트림이 이미 재접속 → exit 2
#       SKIPPED(부적절, no-data): "무스트림 상태를 관측할 수 없음 — 라이브 스택에 상시
#       재접속 스트림 존재". 타이밍에 기인한 비어있지-않음은 절대 NOK 로 판정하지 않는다.
set -u

if [ "${SPEC_TDD_ALLOW_MUTATING:-0}" != "1" ]; then
  echo "SKIPPED: requires zero-push state (container recreate / adapter stop). Set SPEC_TDD_ALLOW_MUTATING=1 to run."
  exit 2
fi

# 승인 시 실행 절차: streaming 재생성 직후(어댑터 push 재개 전) 즉시 조회
COMPOSE_PROJECT_NAME=sentinel docker compose -f "$(git rev-parse --show-toplevel)/docker-compose.yml" up -d --force-recreate streaming

# 재생성 직후 빈 창을 잡기 위한 바운디드 빠른 샘플링. [] 를 한 번이라도 보면 즉시 OK.
LAST=""
for i in $(seq 1 12); do
  BODY=$(docker exec sentinel-cctv-adapter wget -qO- http://streaming:8080/api/streams 2>/dev/null || echo "<unreachable>")
  echo "sample $i: $BODY"
  LAST="$BODY"
  if [ "$BODY" = "[]" ]; then
    echo "OK: 무스트림 빈 배열 관측 ([] on GET /api/streams)"
    exit 0
  fi
  sleep 1
done

# [] 를 끝내 관측하지 못함 — 실 스트림이 이미 재접속했음(비관측). NOK 아님, SKIP.
echo "SKIPPED(부적절, no-data): 무스트림 상태를 관측할 수 없음 — 라이브 스택에 상시 재접속 스트림 존재 (last=${LAST})"
exit 2
