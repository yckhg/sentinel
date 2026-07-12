#!/usr/bin/env bash
# SKIP: mutating — "존재하지 않는 URL 1 + 정상 로컬 파일 1" 픽스처 config로 별도 기동 필요.
#   정상 소스는 프로덕션 streaming에 실제 push를 발생시킴 (설계자 승인 대기).
# youtube-adapter §단언 F (장애 격리·재시도): 30초 내 전자 error+lastError, 후자 running,
#   /healthz 200 유지. 재시도 간격 단조 증가 & ≤30s.
set -u

# 게이트 변수 일관화: SPEC_TDD_ALLOW_MUTATING(스트리밍군 정본) 또는 ALLOW_MUTATING 중
#   하나라도 1 이면 youtube 게이트군(C/F/G/J/J-2)이 함께 켜진다(조용한 부분초록 방지).
if [ "${SPEC_TDD_ALLOW_MUTATING:-0}" != "1" ] && [ "${ALLOW_MUTATING:-0}" != "1" ]; then
  echo "SKIPPED: requires fixture container that pushes a test stream. Set SPEC_TDD_ALLOW_MUTATING=1 to run."
  exit 2
fi

TMPD=$(mktemp -d)
cat > "$TMPD/youtube-sources.json" <<'EOF'
[
 {"id":"bad","youtubeUrl":"https://www.youtube.com/watch?v=zzzzzzzzzzz","streamKey":"spec-f-bad"},
 {"id":"good","youtubeUrl":"https://www.youtube.com/watch?v=placeholder","streamKey":"spec-f-good","localFile":"/media/yt-cam-2.mp4"}
]
EOF
IMG=$(docker inspect sentinel-youtube-adapter --format '{{.Config.Image}}')
docker run -d --rm --name spec-yt-f --network sentinel_sentinel-net \
  -v "$TMPD/youtube-sources.json:/config/youtube-sources.json:ro" \
  -v "$(docker inspect sentinel-youtube-adapter --format '{{range .Mounts}}{{if eq .Destination "/media"}}{{.Source}}{{end}}{{end}}'):/media:ro" \
  "$IMG" >/dev/null
trap 'docker rm -f spec-yt-f >/dev/null 2>&1; rm -rf "$TMPD"' EXIT
sleep 30
S=$(docker exec spec-yt-f wget -qO- http://localhost:8080/api/streams/status)
H=$(docker exec spec-yt-f wget -qO- http://localhost:8080/healthz)
echo "status: $S"; echo "healthz: $H"
printf '%s' "$S" | jq -e '
  (.[]|select(.id=="bad") | .status=="error" and (.lastError|length>0)) and
  (.[]|select(.id=="good") | .status=="running")' >/dev/null \
  || { echo "NOK: isolation not observed"; exit 1; }
printf '%s' "$H" | jq -e '.status=="ok"' >/dev/null || { echo "NOK: healthz degraded"; exit 1; }
echo "retry backoff (from logs, verify monotonic <=30s manually):"
docker logs spec-yt-f 2>&1 | grep -iE 'retry|backoff' | head -10
echo "OK"
exit 0
