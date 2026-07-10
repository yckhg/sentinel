#!/usr/bin/env bash
# SKIP: mutating — B-frame 포함 테스트 push 발행이 필요 (설계자 승인 대기).
#   interface-streaming A1-3 과 동일 쟁점.
# streaming §단언 H (remux-only 코덱 무검사): 허브(nginx-rtmp v1.2.2)는 remux 전용이라
#   B-frame을 거부하지 않고 통과시킨다. -bf 2 push가 지속되고 m3u8이 신선하면 OK.
#   조기 종료(거부)면 허브 동작 변경 → 스펙 재검토(NOK). B-frame 금지는 허브가 아니라
#   push 측(어댑터) 계약이다.
# NOTE(계약 정정, 코드-실측 타이브레이크): 이전 단언 "B-frame push가 ~수초 내 연결 종료되면
#   OK"는 허위 전제였다(domain3 감사: `-bf 2` push 25초 완주·active m3u8). 허브는 코덱을
#   검사하지 않는 remux 이므로, 이 테스트는 "허브가 B-frame을 통과시킴"을 회귀 가드로 재정의한다.
set -u

if [ "${SPEC_TDD_ALLOW_MUTATING:-0}" != "1" ]; then
  echo "SKIPPED: mutating B-frame test push. Set SPEC_TDD_ALLOW_MUTATING=1 to run."
  exit 2
fi

KEY="spec-test-bf"
START=$(date +%s)
# 전체 60초 지속(sustain) 완주를 검증한다 — streaming §H / interface-streaming §A1-3 계약.
# (sibling A1-3_bframe-reject.sh 와 동일한 -t 60 으로 정합.)
timeout 70 docker run --rm --network sentinel_sentinel-net --entrypoint ffmpeg linuxserver/ffmpeg \
  -re -f lavfi -i "testsrc=size=640x360:rate=15" -f lavfi -i sine \
  -t 60 -c:v libx264 -pix_fmt yuv420p -profile:v main -bf 2 -g 30 -c:a aac \
  -f flv "rtmp://streaming:1935/live/${KEY}"
RC=$?
ELAPSED=$(( $(date +%s) - START ))
echo "ffmpeg rc=$RC elapsed=${ELAPSED}s"
# 허브가 B-frame push를 통과시켰다면: 지속(rc=0)되고 m3u8이 신선하다.
M=$(docker exec sentinel-streaming stat -c %Y "/tmp/hls/${KEY}/index.m3u8" 2>/dev/null || echo 0)
NOW=$(date +%s)
if [ "$RC" -eq 0 ] && [ "$ELAPSED" -ge 55 ] && [ "$M" -ne 0 ] && [ $((NOW - M)) -le 15 ]; then
  echo "OK: B-frame push accepted & sustained ~${ELAPSED}s (remux-only, m3u8 fresh) — 허브 코덱 무검사 계약 확인 (60s 완주)"
  exit 0
fi
echo "NOK: B-frame push rejected/terminated (rc=$RC, elapsed=${ELAPSED}s, m3u8 age=$((NOW-M))s) — 60s 완주 실패: nginx-rtmp 동작 변경(코덱 검사 재도입?), 스펙 재검토 필요"
exit 1
