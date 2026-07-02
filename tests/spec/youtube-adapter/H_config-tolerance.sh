#!/usr/bin/env bash
# SKIP: mutating — 깨진/부재 config로 별도 컨테이너 기동 필요 (프로덕션 네트워크에
#   임시 서비스 투입, 설계자 승인 대기).
# youtube-adapter §단언 H (설정 결함 내성): config 없음/깨진 JSON 기동 → 크래시 없이
#   /healthz 200, /api/streams/status == [].
set -u

if [ "${SPEC_TDD_ALLOW_MUTATING:-0}" != "1" ]; then
  echo "SKIPPED: requires throwaway container with broken/missing config."
  exit 2
fi

IMG=$(docker inspect sentinel-youtube-adapter --format '{{.Config.Image}}')
TMPD=$(mktemp -d); echo '{broken' > "$TMPD/youtube-sources.json"
docker run -d --rm --name spec-yt-h1 --network none "$IMG" >/dev/null                       # config 부재
docker run -d --rm --name spec-yt-h2 --network none -v "$TMPD/youtube-sources.json:/config/youtube-sources.json:ro" "$IMG" >/dev/null  # 깨진 JSON
trap 'docker rm -f spec-yt-h1 spec-yt-h2 >/dev/null 2>&1; rm -rf "$TMPD"' EXIT
sleep 3
for C in spec-yt-h1 spec-yt-h2; do
  H=$(docker exec "$C" wget -qO- http://localhost:8080/healthz)
  S=$(docker exec "$C" wget -qO- http://localhost:8080/api/streams/status)
  echo "$C healthz=$H status=$S"
  printf '%s' "$H" | jq -e '.status=="ok"' >/dev/null || { echo "NOK: $C healthz"; exit 1; }
  [ "$S" = "[]" ] || { echo "NOK: $C status not []"; exit 1; }
done
echo "OK"
exit 0
