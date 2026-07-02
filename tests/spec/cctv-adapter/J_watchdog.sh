#!/usr/bin/env bash
# SKIP: mutating — push 프로세스의 출력 정지(hang) 상태를 인위 유발해야 함
#   (프로세스 SIGSTOP 등 프로덕션 컨테이너 내 개입, 설계자 승인 대기). 전제 카메라도 부재.
# cctv-adapter §단언 J (watchdog): FFMPEG_TIMEOUT 초 무출력 프로세스가
#   1.5×FFMPEG_TIMEOUT+15초 이내 종료·교체(PID 변경)되면 OK.
set -u

if [ "${SPEC_TDD_ALLOW_MUTATING:-0}" != "1" ]; then
  echo "SKIPPED: requires inducing ffmpeg output stall inside production container."
  exit 2
fi

T="${FFMPEG_TIMEOUT:-30}"
PID0=$(docker exec sentinel-cctv-adapter sh -c "pidof ffmpeg | awk '{print \$1}'")
[ -n "$PID0" ] || { echo "NOK: no ffmpeg process to test"; exit 1; }
echo "stalling ffmpeg pid=$PID0 with SIGSTOP"
docker exec sentinel-cctv-adapter kill -STOP "$PID0"
DEADLINE=$(( $(date +%s) + (T * 3 / 2) + 15 ))
while [ "$(date +%s)" -le "$DEADLINE" ]; do
  PID1=$(docker exec sentinel-cctv-adapter sh -c "pidof ffmpeg | awk '{print \$1}'" || true)
  if [ -n "$PID1" ] && [ "$PID1" != "$PID0" ]; then
    echo "OK: process replaced ($PID0 -> $PID1) within window"
    exit 0
  fi
  sleep 3
done
docker exec sentinel-cctv-adapter kill -CONT "$PID0" 2>/dev/null || true
echo "NOK: stalled process not replaced within 1.5*T+15s"
exit 1
