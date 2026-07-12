#!/usr/bin/env bash
# B7 (timing/bounded, 핵심 load-bearing) — 아카이브가 종단 상태에 도달하면 UI는 폴링
#   윈도우(web-frontend 상한 5분) 내에 그 종단 상태를 반영하고, 상한 초과 시 "처리 중"에
#   고착되지 않고 중립/새로고침-유도 상태로 전이한다. 폴링 중 5xx/502는 실패 표기가 아니라
#   "처리 중 유지 + 재시도"로 취급.
# SKIP: 브라우저 세션(Playwright) + 라이브 스택(아카이브를 실제 종단까지 구동하는 스테이징
#       recorder)이 함께 있어야 관측 가능. 셸 라이브 API만으로는 판정 불가.
. "$(dirname "$0")/common.sh"
skip_browser B7 "폴링 윈도우 내 종단 상태 반영/타임아웃 전이는 Playwright + 스테이징 recorder(더미 RTMP+격리 볼륨) 필요 — 셸 API 단독 판정 불가"
