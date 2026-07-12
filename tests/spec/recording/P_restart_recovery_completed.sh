#!/usr/bin/env bash
# 단언 P (미완료 아카이브 복구 — 세그먼트 존재 → completed 수렴)
#   docs/spec/recording.md §단언 P / §핵심 로직 7(기동 순서·보호 우선 재확립).
#
# 스펙 정본 절차대로 ROLLING_WINDOW_MINUTES=1 격리 인스턴스에서 실행한다(라이브 미접촉).
#   세 setup(processing / finalizing / pending) 각각에 대해 병합 구간 [from,to) 안에
#   유효 .ts 세그먼트를 심고, 비종단 상태의 metadata.json 을 세팅한 뒤 컨테이너를 재시작해
#   기동 복구를 관측한다.
#
# 판정(세 setup 모두 성립해야 OK):
#   (a) 병합 구간 [from,to) 내 원본 .ts 가 재시작 3분(윈도우의 3배) 후에도 잔존(보호 재확립).
#       — 대조군: 아카이브 밖 미보호 old 세그먼트는 같은 시간에 롤링 삭제됨(cleanup 이 실제로
#         돌았고 보호가 load-bearing 임을 차등 증명; 구간 밖 삭제는 정상·무관).
#   (b) 로그에서 'Recovery protection re-established' 가 'Rolling cleanup started' 보다 먼저.
#       — RC 마커는 첫 cleanup tick(≈+60s)에만 방출되므로 출현할 때까지 폴링(조기 스냅샷 금지).
#   (c) 재시작 60초 이내 completed 로 전이 + sizeBytes>0 + MP4 존재 + ffprobe 유효.
#       — completed 판정은 오직 실 아카이브 상태·실 MP4 로만(로그 위양성 배제).
set -u
cd "$(dirname "$0")"
. ./common.sh
. ./lib-iso-recording.sh

if [ "${ALLOW_MUTATING:-0}" != "1" ]; then
  echo "SKIPPED P: (mutating — 설계자 승인 대기) 격리 recording 인스턴스 기동 + 세그먼트/메타 seed + 재시작 필요 — ALLOW_MUTATING=1 로만 실행"
  exit 2
fi
iso_image_ok || { echo "SKIPPED P: image $ISO_IMG 부재"; exit 2; }

trap iso_stop EXIT

# ── 1) 격리 인스턴스 first-boot ─────────────────────────────────────────────
iso_start
iso_wait_health 45 || { nok "격리 인스턴스 first-boot healthz 미도달"; echo "VERDICT P: NOK"; exit 1; }
info "격리 인스턴스 $ISO_NAME first-boot 완료(window=1m)"

# ── 2) seed: 세 setup × (유효 세그먼트 3개 + 아카이브 메타) + 미보호 대조 세그먼트 ──
iso_make_segment_template || { nok "세그먼트 템플릿 생성 실패(ffmpeg)"; echo "VERDICT P: NOK"; exit 1; }

# 병합 구간: [now-5m, now-90s). 세그먼트는 now-{240,230,220}s → 구간 내 + 윈도우(1m)보다 오래됨.
FROM=$(iso_rfc3339 '-5 minutes'); TO=$(iso_rfc3339 '-90 seconds'); NOW=$(iso_rfc3339 'now')
S1=$(iso_ts '-240 seconds'); S2=$(iso_ts '-230 seconds'); S3=$(iso_ts '-220 seconds')

declare -A KEY=( [processing]=spec-recP-proc-$$ [finalizing]=spec-recP-fin-$$ [pending]=spec-recP-pend-$$ )
declare -A AID
META="["
first=1
for st in processing finalizing pending; do
  k="${KEY[$st]}"
  iso_seed_segment "$k" "$S1"; iso_seed_segment "$k" "$S2"; iso_seed_segment "$k" "$S3"
  aid="arcP_${st}_$$"; AID[$st]="$aid"
  [ $first -eq 1 ] || META="$META,"; first=0
  META="$META{\"id\":\"$aid\",\"incidentId\":\"spec-recP-$$\",\"streamKey\":\"$k\",\"from\":\"$FROM\",\"to\":\"$TO\",\"createdAt\":\"$NOW\",\"sizeBytes\":0,\"filePath\":\"\",\"status\":\"$st\",\"incidentTime\":\"$FROM\"}"
done
META="$META]"

# 대조군: 어떤 아카이브도 참조하지 않는 미보호 old 세그먼트(구간·보호 밖) → cleanup 대상.
CTRL=spec-recP-ctrl-$$
iso_seed_segment "$CTRL" "$S1"

iso_write_metadata "$META"
info "seed 완료: setups=[processing,finalizing,pending] 각 3세그먼트, 대조군=$CTRL"

# 재시작 전 구간 내 세그먼트 목록 스냅샷(setup별)
declare -A BEFORE
for st in processing finalizing pending; do
  BEFORE[$st]=$(iso_exec "ls $ISO_REC/${KEY[$st]} 2>/dev/null" | tr -d '\r')
done

# ── 3) 재시작 → 기동 복구 트리거 ────────────────────────────────────────────
RESTART_AT=$(date +%s)
docker restart "$ISO_NAME" >/dev/null
info "docker restart $ISO_NAME (기동 복구 트리거)"

# ── (b) 마커 순서: RC 가 로그에 출현할 때까지 폴링(최대 ~100s) 후 RE<RC 판정 ──
RE=""; RC=""
for _ in $(seq 1 50); do
  LOGS=$(docker logs "$ISO_NAME" 2>&1)
  BOOT=$(printf '%s\n' "$LOGS" | grep -n 'Recording service starting' | tail -1 | cut -d: -f1)
  SLICE=$(printf '%s\n' "$LOGS" | tail -n +"${BOOT:-1}")
  RE=$(printf '%s\n' "$SLICE" | grep -n 'Recovery protection re-established' | head -1 | cut -d: -f1)
  RC=$(printf '%s\n' "$SLICE" | grep -n 'Rolling cleanup started'          | head -1 | cut -d: -f1)
  [ -n "$RC" ] && break
  sleep 2
done
if [ -n "$RE" ] && [ -n "$RC" ] && [ "$RE" -lt "$RC" ]; then
  ok "(b) 'Recovery protection re-established'(#$RE) < 'Rolling cleanup started'(#$RC) — 보호 우선 순서 계약"
else
  nok "(b) 순서 계약 위배/마커 부재: RE=${RE:-none} RC=${RC:-none}"
fi

# ── (c) 세 setup 모두 60초 이내 completed + MP4 + ffprobe 유효 ───────────────
iso_wait_health 45 || nok "재시작 후 healthz 미도달"
for st in processing finalizing pending; do
  aid="${AID[$st]}"; STA=""; SZ=0; FP=""
  end=$((SECONDS+65))
  while [ "$SECONDS" -lt "$end" ]; do
    STA=$(iso_archive_field "$aid" status)
    SZ=$(iso_archive_field "$aid" sizeBytes); FP=$(iso_archive_field "$aid" filePath)
    { [ "$STA" = "completed" ] && [ "${SZ:-0}" -gt 0 ]; } && break
    [ "$STA" = "failed" ] && break
    sleep 3
  done
  if [ "$STA" = "completed" ] && [ "${SZ:-0}" -gt 0 ]; then
    ok "(c)[$st] completed 수렴: sizeBytes=$SZ"
    MP4SZ=$(iso_exec "stat -c %s '$FP' 2>/dev/null || wc -c < '$FP'" | tr -d ' \r')
    { [ -n "$MP4SZ" ] && [ "${MP4SZ:-0}" -gt 0 ]; } && ok "(c)[$st] MP4 존재: $FP ($MP4SZ bytes)" || nok "(c)[$st] MP4 미존재/0바이트: $FP"
    if iso_exec "ffprobe -v error -show_entries stream=codec_type -of csv=p=0 '$FP'" 2>/dev/null | grep -q video; then
      ok "(c)[$st] ffprobe 유효(video 스트림 확인)"
    else
      nok "(c)[$st] ffprobe 무효(video 스트림 미확인) — MP4 손상"
    fi
  else
    nok "(c)[$st] completed 미수렴: status=$STA sizeBytes=${SZ:-0} (유효 세그먼트는 completed 로만 수렴해야 함)"
  fi
done

# ── (a) 3분(3×window) 후 구간 내 세그먼트 잔존 + 대조군 삭제 ─────────────────
WAIT=$(( 180 - ( $(date +%s) - RESTART_AT ) ))
[ "$WAIT" -gt 0 ] && { info "(a) 3×window(180s) 경과 대기 ${WAIT}s ..."; sleep "$WAIT"; }

for st in processing finalizing pending; do
  k="${KEY[$st]}"
  after=$(iso_exec "ls $ISO_REC/$k 2>/dev/null" | tr -d '\r')
  miss=""
  for seg in ${BEFORE[$st]}; do
    printf '%s\n' "$after" | grep -qx "$seg" || miss="$miss $seg"
  done
  if [ -z "$miss" ]; then
    ok "(a)[$st] 병합 구간 세그먼트 전부 3분 후 잔존 — 보호 재확립"
  else
    nok "(a)[$st] 병합 구간 세그먼트 소실(보호 재확립 실패):$miss"
  fi
done
# 대조군은 삭제되어야 cleanup 이 실제로 돌았음(보호가 load-bearing)을 증명
if iso_exec "ls $ISO_REC/$CTRL/$S1.ts 2>/dev/null" | grep -q .; then
  nok "(a)[대조] 미보호 old 세그먼트가 잔존 — cleanup 미동작 의심(보호 차등 증명 실패)"
else
  ok "(a)[대조] 미보호 old 세그먼트 롤링 삭제 확인 — cleanup 동작 + 보호 load-bearing 차등 증명"
fi

if [ "$FAILED" -eq 0 ]; then echo "VERDICT P: OK"; exit 0; else echo "VERDICT P: NOK"; exit 1; fi
