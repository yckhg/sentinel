#!/usr/bin/env bash
# K. 비밀정보 마스킹 (핵심, 보안): 설정된 자격증명 평문이 notifier 로그에 0건.
# spec: docs/spec/notifier.md — 검증 단언 K (§출력 9)
# 판정: OK=exit0 (매칭 0건), NOK=exit1 (평문 1건 이상), SKIPPED=exit2.
# non-vacuity: 설정된(비어있지 않은) 자격증명 표본 수 N을 반드시 출력.
set -uo pipefail

# 컨테이너 env에서 자격증명 값을 읽는다 (호스트 미오염).
getenv() {
  docker inspect sentinel-notifier --format "{{range .Config.Env}}{{println .}}{{end}}" 2>/dev/null \
    | grep -E "^$1=" | head -1 | cut -d= -f2-
}
# Docker 내부망에서 notifier로 직접 주입 (curl은 일회성 컨테이너에 설치 — 호스트 미오염)
ncurl() {
  docker run --rm --network sentinel_sentinel-net alpine:3.19 sh -c \
    "apk add -q curl >/dev/null && curl $*"
}

# 설정된(비어있지 않은) 자격증명만 표본으로 수집.
declare -a SECRETS=()
for k in KAKAO_API_KEY KAKAO_SENDER_KEY NHN_SMS_APP_KEY NHN_SMS_SECRET_KEY SMTP_PASS; do
  v=$(getenv "$k")
  [ -n "$v" ] && SECRETS+=("$v")
done
N=${#SECRETS[@]}
echo "checked $N secrets"

# 자격증명이 하나도 설정 안 됐으면 non-vacuous하게 판정 불가.
if [ "$N" -eq 0 ]; then
  echo "SKIPPED (부적절, no-data): 설정된 자격증명 0건 — 마스킹을 관측할 표본 없음"
  exit 2
fi

# 설정된 시크릿이 있으면 발송 경로를 1회 태운다 (성공/실패 무관). 실제 발송(이메일/알람)을
# 유발하므로 mutating — 프로덕션 실행은 ALLOW_MUTATING=1 게이트로만.
if [ "${ALLOW_MUTATING:-0}" = "1" ]; then
  ncurl "-s -o /dev/null -X POST http://notifier:8080/api/notify -H \"Content-Type: application/json\" \
    -d \"{\\\"siteId\\\":\\\"site1\\\",\\\"deviceId\\\":\\\"TEST-K1\\\",\\\"type\\\":\\\"gas_leak\\\",\\\"timestamp\\\":\\\"$(date -u +%Y-%m-%dT%H:%M:%SZ)\\\",\\\"test\\\":true}\""
  sleep 8
  echo "injected=1 (발송 경로 exercise)"
else
  echo "injected=0 (ALLOW_MUTATING!=1 — 기존 로그에 대한 마스킹 회귀 점검만 수행)"
fi

# notifier 전체 로그에서 각 자격증명 평문을 고정문자열(grep -F)로 검색.
log=$(docker logs sentinel-notifier 2>&1)
match=0
for s in "${SECRETS[@]}"; do
  c=$(printf '%s' "$log" | grep -F -c -- "$s" 2>/dev/null || true)
  [ "$c" -gt 0 ] && { echo "NOK: 자격증명 평문 노출 ${c}건 (앞4자 $(printf '%.4s' "$s")***)"; match=$((match+c)); }
done

echo "matches=$match (checked $N secrets)"
[ "$match" -eq 0 ] && { echo OK; exit 0; } || { echo "NOK: 자격증명 평문이 로그에 출현"; exit 1; }
