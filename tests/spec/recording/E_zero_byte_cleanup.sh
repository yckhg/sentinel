#!/usr/bin/env bash
# 단언 E. 0바이트 정리 — 0바이트 .ts를 만들어 두면 60초 이내 삭제된다.
# SKIP: 스트림 디렉터리에 0바이트 .ts "생성"이 필요 — 세그먼트 파일 생성은 MUTATING
#       (프로덕션 증거 저장소 오염 금지). 설계자 승인(ALLOW_MUTATING=1) 대기.
# 보조 관측(READ-ONLY): mtime 60초보다 오래된 0바이트 .ts가 현재 하나도 없는지
#       (있다면 정리 로직 미동작의 반증 → NOK).
. "$(dirname "$0")/common.sh"

stale0=$(rexec "find $RECORDINGS_DIR -name '*.ts' -size 0 -mmin +2 2>/dev/null" | grep -c . || true)
if [ "$stale0" -gt 0 ]; then
  rexec "find $RECORDINGS_DIR -name '*.ts' -size 0 -mmin +2" | head -5 | sed 's/^/  [NOK] 잔존 0바이트: /'
  echo "VERDICT E: NOK — 2분 이상 경과한 0바이트 .ts ${stale0}개 잔존 (60초 내 삭제 계약 위배)"
  exit 1
fi
info "2분 이상 경과한 0바이트 .ts 없음 (약한 정합 — 생성 자체가 없었을 수도 있음)"

if [ "${ALLOW_MUTATING:-0}" != "1" ]; then
  skip_mutating E "0바이트 .ts 생성 필요(파일 생성 금지); 관측상 잔존 0바이트 파일은 없음"
fi

# ---- MUTATING PART (승인 시에만) ----
k=$(active_key); f="$RECORDINGS_DIR/$k/00010101_000000.ts"
rexec "touch $f"
sleep 60
if rexec "test -f $f"; then echo "VERDICT E: NOK — 60초 후에도 잔존"; rexec "rm -f $f"; exit 1; fi
echo "VERDICT E: OK"
