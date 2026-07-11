#!/usr/bin/env bash
# youtube-adapter §단언 J-2 (무효 인코딩 값 폴백) — docs/spec/youtube-adapter.md §단언 J-2.
#   무효 ENCODE_GOP=xyz(+무효 ENCODE_PRESET)로 기동 → (a) 크래시 루프 없이 /healthz 200 유지,
#   (b) 실행 중 ffmpeg 인자에 기본값 -g 60 / -preset ultrafast 로 폴백 + 경고 로그,
#   (c) 30초 이상 송출 유지 + §단언 E(-c:v libx264 + -c:a aac) 성립.
#
# MUTATING: throwaway 컨테이너 기동 + 스트림 송출. ALLOW_MUTATING=1 필요.
#   프로덕션 오염 방지: 격리 RTMP 싱크(별도 streaming 인스턴스)로만 송출.
set -u

IMG="${YT_IMG:-sentinel-youtube-adapter:latest}"
SINK_IMG="${SINK_IMG:-sentinel-streaming:latest}"
NET="${NET:-sentinel_sentinel-net}"

skip() { echo "SKIPPED J-2: $*"; exit 2; }
ok()   { echo "  [ok]  $*"; }
nok()  { echo "  [NOK] $*"; FAIL=1; }
info() { echo "  [..]  $*"; }

if [ "${ALLOW_MUTATING:-0}" != "1" ]; then
  skip "(mutating — 설계자 승인 대기) throwaway 무효-env 컨테이너 기동 필요 — ALLOW_MUTATING=1 로만 실행"
fi
docker image inspect "$IMG" >/dev/null 2>&1 || skip "image $IMG 부재"

FAIL=0
TMPD=$(mktemp -d)
SINK="spec-encsink2-$$"; AC="spec-encj2-$$"
cleanup() { docker rm -f "$SINK" "$AC" >/dev/null 2>&1; rm -rf "$TMPD"; }
trap cleanup EXIT

docker run --rm -v "$TMPD":/media --network none "$IMG" \
  ffmpeg -hide_banner -loglevel error -f lavfi -i testsrc=size=320x240:rate=15 \
  -f lavfi -i sine=frequency=440 -t 3 -c:v libx264 -pix_fmt yuv420p -c:a aac -shortest /media/src.mp4 \
  || skip "테스트 소스 생성 실패 (ffmpeg lavfi 부재?)"
# 유효 소스: loadConfig 의 youtubeUrl 검증(⚠#2)을 통과시키되 localFile 우선 오프라인 재생으로
#   실 ffmpeg 을 네트워크 없이 기동. (localFile-only 면 기동 시 탈락 → ffmpeg 미실행 → false-NOK)
printf '[{"id":"enc","youtubeUrl":"https://youtu.be/specencinv","streamKey":"spec-enc-inv","localFile":"/media/src.mp4"}]' > "$TMPD/cfg.json"

docker run -d --rm --name "$SINK" --network "$NET" "$SINK_IMG" >/dev/null 2>&1 \
  && info "격리 RTMP 싱크 $SINK 기동" || info "싱크 기동 실패 — ffmpeg 재시도 창에서 관측 시도"
sleep 3

# 무효값 주입: ENCODE_GOP=xyz(정수 아님) + ENCODE_PRESET=boguspreset(미인식)
docker run -d --rm --name "$AC" --network "$NET" \
  -e STREAMING_RTMP_URL="rtmp://$SINK:1935/live" \
  -e ENCODE_GOP=xyz -e ENCODE_PRESET=boguspreset \
  -v "$TMPD/src.mp4":/media/src.mp4:ro -v "$TMPD/cfg.json":/config/youtube-sources.json:ro \
  "$IMG" >/dev/null || { nok "무효-env 컨테이너 기동 실패"; echo "VERDICT J-2: NOK"; exit 1; }

# (a) 크래시 루프 없음 + /healthz 200 유지 (~33초에 걸쳐 반복 표본)
HEALTH_OK=1; RUN_OK=1
BLOB=""
for i in $(seq 1 11); do
  RUNNING=$(docker inspect "$AC" --format '{{.State.Running}}' 2>/dev/null)
  [ "$RUNNING" = "true" ] || RUN_OK=0
  H=$(docker exec "$AC" wget -qO- http://localhost:8080/healthz 2>/dev/null)
  printf '%s' "$H" | grep -q '"status":"ok"' || HEALTH_OK=0
  # 실 ffmpeg 프로세스 인자만 수집(busybox ps = 전체 cmdline). 로그 라인 grep 은
  #   실행 여부와 무관한 위양성이라 제거 — 폴백은 실프로세스의 -g/-preset 으로만 판정.
  BLOB="$BLOB
$(docker exec "$AC" ps 2>/dev/null | grep -i ffmpeg | grep -v grep)"
  sleep 3
done
RCOUNT=$(docker inspect "$AC" --format '{{.RestartCount}}' 2>/dev/null)
[ "$RUN_OK" = "1" ] && [ "${RCOUNT:-0}" = "0" ] && ok "(a) 컨테이너 상시 가동 (RestartCount=$RCOUNT) — 크래시 루프 없음" \
                                                 || nok "(a) 크래시 루프 의심: Running 표본 실패 또는 RestartCount=${RCOUNT:-?}"
[ "$HEALTH_OK" = "1" ] && ok "(a) /healthz 200(ok) 33초간 유지" || nok "(a) /healthz 표본 중 비-ok 관측"

# (b) 폴백 + 경고 로그
chk() { if printf '%s' "$2" | grep -qE -- "$1"; then ok "$3"; else nok "$3 (미관측)"; fi; }
printf '%s' "$BLOB" | grep -qi 'ffmpeg' || nok "(b) ffmpeg 실프로세스 미관측 — 스트림 미구동"
chk ' -g 60( |$)'            "$BLOB" "(b) 무효 GOP → 기본 -g 60 폴백"
chk ' -preset ultrafast( |$)' "$BLOB" "(b) 무효 preset → 기본 -preset ultrafast 폴백"
# 인코딩 폴백 경고만 정확 매칭 — 소스거부 경고("Skipping source ... invalid YouTube URL")와 구별.
#   코드 문구: "invalid ENCODE_GOP ... falling back to default", "invalid ENCODE_PRESET ... falling back to default".
WARN=$(docker logs "$AC" 2>&1 | grep -iE 'invalid ENCODE_(GOP|PRESET).*falling back to default' | grep -vi 'Skipping source' | head -3)
[ -n "$WARN" ] && ok "(b) 인코딩 폴백 경고 로그 존재: $(printf '%s' "$WARN" | head -1)" || nok "(b) 인코딩 폴백 경고 로그 부재(소스거부 경고 오탐 배제)"

# (c) 30초 이상 송출 유지 + E(libx264+aac)
chk ' -c:v libx264( |$)' "$BLOB" "(c) E: -c:v libx264"
chk ' -c:a aac( |$)'     "$BLOB" "(c) E: -c:a aac"
STARTED=$(docker exec "$AC" wget -qO- http://localhost:8080/api/streams/status 2>/dev/null \
  | jq -r '[.[]|select(.status=="running")][0].startedAt // empty' 2>/dev/null)
if [ -n "$STARTED" ]; then
  AGE=$(( $(date +%s) - $(date -d "$STARTED" +%s) ))
  [ "$AGE" -ge 30 ] && ok "(c) 송출 세션 ${AGE}s ≥ 30s 유지" || nok "(c) 송출 세션 ${AGE}s < 30s"
else
  nok "(c) running 스트림 startedAt 미확인 — 30초 송출 유지 판정 불가"
fi

if [ "$FAIL" -eq 0 ]; then echo "VERDICT J-2: OK"; exit 0; else echo "VERDICT J-2: NOK"; exit 1; fi
