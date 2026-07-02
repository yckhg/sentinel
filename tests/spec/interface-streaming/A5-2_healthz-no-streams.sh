#!/usr/bin/env bash
# SKIP: mutating — "push가 하나도 없는 상태"를 만들려면 어댑터 중지/컨테이너 재생성 필요
#   (설계자 승인 대기). 현재 프로덕션에는 활성 push가 존재하여 전제 상태를 만들 수 없음.
# A5-2 (스트림 무관): push 0개 상태에서도 A5-1 이 통과하면 OK.
set -u

if [ "${SPEC_TDD_ALLOW_MUTATING:-0}" != "1" ]; then
  echo "SKIPPED: requires zero-push state. Set SPEC_TDD_ALLOW_MUTATING=1 to run."
  exit 2
fi

# 승인 시: 어댑터 정지 → 40초 대기(active 전환) → healthz 판정 → 어댑터 재기동
docker stop sentinel-youtube-adapter sentinel-cctv-adapter
trap 'docker start sentinel-youtube-adapter sentinel-cctv-adapter >/dev/null' EXIT
sleep 40
BODY=$(docker exec sentinel-streaming wget -qO- http://localhost:8080/healthz)
echo "body: $BODY"
printf '%s' "$BODY" | jq -e '.status=="ok" and .service=="streaming"' >/dev/null \
  && { echo "OK"; exit 0; }
echo "NOK"
exit 1
