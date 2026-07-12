#!/usr/bin/env bash
# 단언 P-2 (미완료 아카이브 복구 — 세그먼트 소실 → failed 종단 강제)
#   docs/spec/recording.md §단언 P-2 / §핵심 로직 7.
#
# 스펙 정본 절차대로 ROLLING_WINDOW_MINUTES=1 격리 인스턴스에서 실행한다(라이브 미접촉).
#   비종단 3종(processing / finalizing / pending) 각각의 아카이브를 metadata.json 에 심되
#   병합 구간 [from,to) 의 원본 .ts 는 부재(빈 스트림 디렉터리)로 두어 "세그먼트 소실"을
#   비파괴로 재현하고 재시작한다.
#
# 판정(세 setup 모두):
#   재시작 60초 이내에 아카이브가 failed 로 종단 전이 + 사유(error/lastError) 비어있지 않음.
#   completed(불가능한 성공 표기)거나 60초 초과 비종단 고착이면 NOK.
set -u
cd "$(dirname "$0")"
. ./common.sh
. ./lib-iso-recording.sh

if [ "${ALLOW_MUTATING:-0}" != "1" ]; then
  echo "SKIPPED P-2: (mutating — 설계자 승인 대기) 격리 recording 인스턴스 기동 + 메타 seed(소실) + 재시작 필요 — ALLOW_MUTATING=1 로만 실행"
  exit 2
fi
iso_image_ok || { echo "SKIPPED P-2: image $ISO_IMG 부재"; exit 2; }

trap iso_stop EXIT

# ── 1) 격리 인스턴스 first-boot ─────────────────────────────────────────────
iso_start
iso_wait_health 45 || { nok "격리 인스턴스 first-boot healthz 미도달"; echo "VERDICT P-2: NOK"; exit 1; }
info "격리 인스턴스 $ISO_NAME first-boot 완료(window=1m)"

# ── 2) seed: 세 비종단 상태 아카이브 × 세그먼트 부재(빈 디렉터리) ────────────
FROM=$(iso_rfc3339 '-3 hours'); TO=$(iso_rfc3339 '-2 hours -55 minutes'); NOW=$(iso_rfc3339 'now')
declare -A KEY=( [processing]=spec-recP2-proc-$$ [finalizing]=spec-recP2-fin-$$ [pending]=spec-recP2-pend-$$ )
declare -A AID
META="["
first=1
for st in processing finalizing pending; do
  k="${KEY[$st]}"
  iso_exec "mkdir -p $ISO_REC/$k"      # 빈 디렉터리 — 구간 내 .ts 전무(소실 재현)
  aid="arcP2_${st}_$$"; AID[$st]="$aid"
  [ $first -eq 1 ] || META="$META,"; first=0
  META="$META{\"id\":\"$aid\",\"incidentId\":\"spec-recP2-$$\",\"streamKey\":\"$k\",\"from\":\"$FROM\",\"to\":\"$TO\",\"createdAt\":\"$NOW\",\"sizeBytes\":0,\"filePath\":\"\",\"status\":\"$st\",\"incidentTime\":\"$FROM\"}"
done
META="$META]"
iso_write_metadata "$META"
info "seed 완료: 비종단 3종(processing,finalizing,pending) × 세그먼트 부재"

# ── 3) 재시작 → 기동 복구 트리거 ────────────────────────────────────────────
docker restart "$ISO_NAME" >/dev/null
info "docker restart $ISO_NAME (기동 복구 트리거)"
iso_wait_health 45 || nok "재시작 후 healthz 미도달"

# ── 판정: 세 setup 모두 60초 이내 failed(+사유) 종단 ────────────────────────
for st in processing finalizing pending; do
  aid="${AID[$st]}"; STA=""; ERR=""
  end=$((SECONDS+65))
  while [ "$SECONDS" -lt "$end" ]; do
    STA=$(iso_archive_field "$aid" status)
    { [ "$STA" = "failed" ] || [ "$STA" = "completed" ]; } && break
    sleep 3
  done
  ERR=$(iso_archive_field "$aid" error); [ -n "$ERR" ] || ERR=$(iso_archive_field "$aid" lastError)
  case "$STA" in
    failed)
      ok "[$st] 60초 이내 failed 종단 전이"
      [ -n "$ERR" ] && ok "[$st] 사유 비어있지 않음: $ERR" || nok "[$st] failed 이나 사유 부재 (P-2 는 사유 필수)"
      ;;
    completed)
      nok "[$st] 세그먼트 부재인데 completed — 불가능한 성공 표기 (P-2 위배)" ;;
    processing|pending|finalizing)
      nok "[$st] 60초 초과 비종단 고착: status=$STA (재시작 넘겨 고착 금지 — 반드시 failed+사유)" ;;
    *)
      nok "[$st] 판정 불가: status='${STA:-none}' (seed 아카이브 미발견?)" ;;
  esac
done

if [ "$FAILED" -eq 0 ]; then echo "VERDICT P-2: OK"; exit 0; else echo "VERDICT P-2: NOK"; exit 1; fi
