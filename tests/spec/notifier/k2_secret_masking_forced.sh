#!/usr/bin/env bash
# K2. 비밀정보 마스킹 — 전 클래스 실패-경로 강제 실증 (단언 K, HIGH).
# spec: docs/spec/notifier.md — 검증 단언 K (§출력 9)
#
# 라이브 sentinel-notifier 는 채널 비활성 + 자격증명 공백이라 K가 vacuous 하게만 성립한다.
# 여기서는 라이브 스택을 오염시키지 않고, 이미 빌드된 sentinel-notifier 이미지를 재사용해
# **격리 일회용 컨테이너**를 띄운다. 가짜지만 grep 가능한 고유 시크릿(SPEC*_deadbeef*)과
# 도달 불가 엔드포인트를 주입해 Kakao/SMS/SMTP 전 채널이 반드시 transport 실패 경로를 타게
# 강제한다(--add-host 로 SMS의 하드코딩 URL(api-sms.cloud.toast.com, APP_KEY 박힘)도 127.0.0.1
# 로 몰아 url.Error 에 APP_KEY 가 실리는 정확한 누출 경로를 재현).
#
# non-vacuity 가드(Fix A): grep-0 은 실패-경로가 실제로 안 돌아도(0연락처/web-backend 다운)
#   trivially 참이 된다. 따라서 Kakao/SMS FAILED 로그 + SMS URL 누출경로(appKeys/.../sender/sms)
#   가 로그에 실제 관측될 것을 OK의 필수 전제로 요구한다. 증거 부재 시 SKIPPED(no-dispatch).
# side-effect 격리(Fix D): RECORDING_URL 을 도달불가(non-empty)로 지정해 라이브 recording 에
#   protect 요청이 안 닿게 한다. 연락처 조회(read-only)만 라이브 web-backend 를 쓴다 — 그로 인해
#   temp-link 발급/system_alarm outbound 도 라이브 web-backend 로 나가지만, 둘 다 무해하다
#   (alarm = admin WS 브로드캐스트, DB 영속 없음 / temp-link = 만료성 링크). 이 트레이드오프는
#   같은 base URL 을 공유하는 구조상 불가피하다.
# 판정: OK=exit0, NOK=exit1, SKIPPED=exit2. 표본수(주입 n / 런타임 loggable 도달 n) 정직 보고.
set -uo pipefail

CNAME=notifier-spec-forced
NET=sentinel_sentinel-net
IMG=sentinel-notifier:latest

cleanup() { docker rm -f "$CNAME" >/dev/null 2>&1 || true; }
trap cleanup EXIT
cleanup

ncurl() { # 인접 일회성 컨테이너에서 curl (호스트 미오염)
  docker run --rm --network "$NET" alpine:3.19 sh -c "apk add -q curl >/dev/null && curl $*"
}

docker image inspect "$IMG" >/dev/null 2>&1 || { echo "SKIPPED: $IMG 이미지 없음 — 먼저 build 필요"; exit 2; }

# 고유 시크릿 5종 (grep 관측용).
S_KKEY=SPECKKEY_deadbeef01
S_KSEND=SPECKSENDER_deadbeef02
S_APP=SPECAPP_deadbeef03
S_SEC=SPECSEC_deadbeef04
S_SMTP=SPECSMTP_deadbeef05
SECRETS=("$S_KKEY" "$S_KSEND" "$S_APP" "$S_SEC" "$S_SMTP")
N=${#SECRETS[@]}
echo "injected $N secrets (5 클래스: KakaoAPIKey/SenderKey, NHNAppKey/SecretKey, SMTPPass)"

# 격리 notifier 기동: 모든 외부 엔드포인트 도달 불가 → 전송이 반드시 실패 경로.
# SMS URL 은 코드에 하드코딩(api-sms.cloud.toast.com)이므로 --add-host 로 127.0.0.1 강제.
# RECORDING_URL 은 도달불가(non-empty) — 라이브 recording 무오염(Fix D).
docker run -d --name "$CNAME" --network "$NET" \
  --add-host api-sms.cloud.toast.com:127.0.0.1 \
  -e KAKAO_ENABLED=true -e SMS_ENABLED=true \
  -e KAKAO_API_URL=http://127.0.0.1:9/nowhere -e KAKAO_API_KEY="$S_KKEY" \
  -e KAKAO_SENDER_KEY="$S_KSEND" -e KAKAO_TEMPLATE_CODE=T1 \
  -e NHN_SMS_APP_KEY="$S_APP" -e NHN_SMS_SECRET_KEY="$S_SEC" -e NHN_SMS_SENDER_NO=000 \
  -e SMTP_HOST=127.0.0.1 -e SMTP_PORT=9 -e SMTP_USER=u -e SMTP_PASS="$S_SMTP" -e SMTP_FROM=u@x \
  -e WEB_BACKEND_URL=http://web-backend:8080 -e RECORDING_URL=http://127.0.0.1:9 \
  "$IMG" >/dev/null || { echo "NOK: 격리 컨테이너 기동 실패"; exit 1; }

# healthz 대기 (최대 ~15s).
ok=0
for _ in $(seq 1 15); do
  code=$(ncurl "-s -o /dev/null -w %{http_code} http://$CNAME:8080/healthz" 2>/dev/null || true)
  [ "$code" = 200 ] && { ok=1; break; }
  sleep 1
done
[ "$ok" = 1 ] || { echo "NOK: 격리 notifier healthz 미기동"; docker logs "$CNAME" 2>&1 | tail -5; exit 1; }

TS=$(date -u +%Y-%m-%dT%H:%M:%SZ)
# 1) /api/notify — 연락처(라이브 web-backend)≥1 이면 Kakao→SMS 실패-경로를 태운다.
ncurl "-s -o /dev/null -X POST http://$CNAME:8080/api/notify -H \"Content-Type: application/json\" \
  -d \"{\\\"siteId\\\":\\\"site1\\\",\\\"deviceId\\\":\\\"TEST-K2\\\",\\\"type\\\":\\\"gas_leak\\\",\\\"timestamp\\\":\\\"$TS\\\",\\\"test\\\":true}\"" >/dev/null 2>&1
# 2) /api/send-email — SMTP(127.0.0.1:9) 실패-경로로 SMTP_PASS 경로도 태운다(내부망 IP → 허용).
ncurl "-s -o /dev/null -X POST http://$CNAME:8080/api/send-email -H \"Content-Type: application/json\" \
  -d \"{\\\"to\\\":\\\"a@b.c\\\",\\\"subject\\\":\\\"s\\\",\\\"body\\\":\\\"<p>x</p>\\\"}\"" >/dev/null 2>&1

# 전송 타임아웃/재시도 여유 (도달 불가라 대부분 즉시 실패, 여유 확보).
sleep 12

log=$(docker logs "$CNAME" 2>&1)

# --- Fix A: non-vacuity 증거 (실패-경로 실제 실행) ---
kakao_failed=$(printf '%s' "$log" | grep -cE "KakaoTalk FAILED" || true)
sms_failed=$(printf '%s' "$log" | grep -cE "SMS FAILED" || true)
# SMS URL 누출경로가 로그에 실제 도달했는지(마스킹 후엔 appKeys/SPEC***/sender/sms 형태).
sms_url_path=$(printf '%s' "$log" | grep -cE "appKeys/.*/sender/sms" || true)
echo "evidence: KakaoTalk_FAILED=$kakao_failed SMS_FAILED=$sms_failed SMS_URL_path=$sms_url_path"

if [ "$kakao_failed" -lt 1 ] || [ "$sms_failed" -lt 1 ] || [ "$sms_url_path" -lt 1 ]; then
  echo "SKIPPED (부적절, no-dispatch): 실패-경로 미실행 — web-backend 0연락처/다운으로 Kakao/SMS 시도가 안 돌아 grep-0 이 trivial. 연락처≥1 인 라이브 web-backend 필요."
  exit 2
fi
# URL 누출경로가 실제 태워졌으므로 런타임 loggable 도달 시크릿 = NHN app key(URL 삽입) 최소 1건.
echo "runtime loggable-reached secrets = 1 (NHN app key via URL; 나머지 4클래스는 헤더/post-connect 라 실패-경로 문자열엔 미도달 — 단위 TestScrub 로 커버)"

# --- 평문 노출 판정 ---
match=0
for s in "${SECRETS[@]}"; do
  c=$(printf '%s' "$log" | grep -F -c -- "$s" 2>/dev/null || true)
  if [ "$c" -gt 0 ]; then echo "NOK: 시크릿 평문 노출 ${c}건 ($s)"; match=$((match+c)); fi
done

# 마스킹 변환(앞4자 SPEC + ***)이 실제 로그에 관측되는지(진단 표기 실증).
masked=$(printf '%s' "$log" | grep -c -- 'SPEC\*\*\*' 2>/dev/null || true)
echo "masked_form_observed=$masked (SPEC*** 형태)"
echo "matches=$match (injected $N secrets)"

[ "$match" -eq 0 ] || { echo "NOK: 실패-경로에서 자격증명 평문 노출"; exit 1; }
echo "OK"
exit 0
