#!/usr/bin/env bash
# A7 — 종단 단조성: 한 번 completed가 된 아카이브는 이후 조회에서 미완료로 되돌아가지
#      않는다. completedAt 델타 불요(레거시 completed로도 판정 가능)하나, completed
#      대상 항목 자체는 mutating fixture(±스테이징 recorder)로 확보돼야 한다.
# READ-ONLY: 동일 아카이브를 반복 조회해 status가 completed→미완료로 후퇴하지 않음을 관측.
. "$(dirname "$0")/common.sh"
require_container A7

samples="${A7_SAMPLES:-4}"; interval="${A7_INTERVAL:-2}"
declare -A first_completed

seen_completed=0
for i in $(seq 1 "$samples"); do
  tmp=$(mktemp); archives_json "$tmp"
  while IFS= read -r id; do
    [ -z "$id" ] && continue
    first_completed["$id"]=1; seen_completed=1
  done < <(ids_with_status "$tmp" completed)

  # 이전에 completed로 관측된 id가 지금 미완료로 후퇴했는지 검사.
  for id in "${!first_completed[@]}"; do
    cur=$(python3 -c "import json;print(next((x['status'] for x in json.load(open('$tmp')) if x['id']=='$id'),'ABSENT'))")
    case "$cur" in
      completed|ABSENT|failed) : ;; # completed 유지 / 삭제 / (모순 아님: completed는 failed로 안 감—아래서 별도 체크)
      *) nok "아카이브 $id 가 completed→$cur 로 후퇴(단조성 위반)";;
    esac
    if [ "$cur" = "failed" ]; then nok "아카이브 $id 가 completed→failed 로 전이(종단 뒤집힘)"; fi
  done
  rm -f "$tmp"
  [ "$i" -lt "$samples" ] && sleep "$interval"
done

if [ "$seen_completed" = "0" ]; then
  skip_staging A7 "반복 조회 윈도우 내 completed 아카이브 미관측 — 판정 대상 없음(mutating fixture 필요)"
fi
[ "$FAILED" -eq 0 ] && ok "반복 조회 ${samples}회: completed 항목의 미완료 후퇴 없음"
verdict A7
