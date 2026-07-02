#!/usr/bin/env bash
# docs/spec/recording.md "검증 단언 (TDD)" 전체 실행기.
# 기본은 READ-ONLY/사후 관측만 수행 — MUTATING 단계는 각 스크립트가 자체 SKIP.
# (ALLOW_MUTATING=1 은 설계자 승인 후 비프로덕션에서만 사용할 것)
set -u
cd "$(dirname "$0")"
for t in A_healthz B_segment_creation C_status_report D_rolling_delete \
         E_zero_byte_cleanup F_protect_blocks_delete G_finalize_mp4 H_finalize_gate \
         I_playback_contract J_path_escape K_time_ranges L_reload_reconcile \
         M_auto_finalize N_storage_stats O_restart_persistence; do
  echo "=== $t ==="
  bash "./$t.sh"
  echo
done
