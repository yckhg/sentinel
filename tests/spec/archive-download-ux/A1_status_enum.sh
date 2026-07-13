#!/usr/bin/env bash
# A1 (핵심) — GET /api/archives 각 항목의 status는 6종 enum 중 하나. enum 밖 값 없음.
# vacuity 가드: 목록이 0개면 판정 불가(전제 미충족). READ-ONLY.
. "$(dirname "$0")/common.sh"
require_container A1

tmp=$(mktemp); archives_json "$tmp"
python3 -c "
import json,sys
data=json.load(open('$tmp'))
if not isinstance(data,list) or len(data)==0:
    print('  [..]  아카이브 목록 0개 — 판정 불가(전제 미충족, vacuity 가드)'); sys.exit(2)
enum={'protecting','pending','finalizing','processing','completed','failed'}
bad=[x.get('status') for x in data if x.get('status') not in enum]
if bad:
    print('  [NOK] enum 밖 status 관측: %r' % sorted(set(bad))); sys.exit(1)
print('  [ok]  %d개 항목 모두 6종 enum 내 status' % len(data)); sys.exit(0)
"
rc=$?; rm -f "$tmp"
case "$rc" in
  0) echo "VERDICT A1: OK";;
  2) echo "VERDICT A1: SKIPPED (전제 미충족 — 아카이브 목록 0개)";;
  *) echo "VERDICT A1: NOK"; exit 1;;
esac
