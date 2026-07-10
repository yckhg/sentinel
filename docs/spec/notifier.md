# notifier 스펙

> 상태: 살아있는 계약 (living spec) · 독자: 스펙 작성자

## 목적 / 의도

- notifier는 산업안전 위기(crisis) 이벤트의 **인명 전파 최종 구간**이다. hw-gateway가 넘겨준 위기 이벤트를 받아, 등록된 모든 비상연락처에게 다채널(KakaoTalk → SMS → 웹 시스템 알람) fallback으로 전달을 시도한다.
- 핵심 의도는 두 가지다:
  1. **유실 방지** — 유효한 위기 이벤트가 접수되면, 각 연락처에 대해 반드시 "외부 채널 성공" 또는 "실패의 로그 기록(+ 시스템 알람 기록 시도)" 중 하나의 관측 가능한 결과가 남아야 한다. 조용히 사라지는 경로(silent fail)는 허용되지 않으며, 모든 실패는 로그에 남는다.
  2. **비차단** — 호출자(hw-gateway)를 절대 기다리게 하지 않는다. 접수 즉시 응답하고 발송은 비동기로 수행한다.
- 부가 의도: 위기 발생 시점의 CCTV 영상을 볼 수 있는 임시 링크를 알림에 포함시키고, 해당 인시던트 시간대의 녹화 세그먼트가 삭제되지 않도록 보호를 요청한다.

## 언어 · 런타임

- Go 단일 바이너리, 표준 라이브러리만 사용 (외부 Go 의존성 없음).
- Docker 컨테이너로 구동, 내부 포트 `:8080`.
- 완전한 stateless — 영구 저장소 없음. 전송 이력의 유일한 1차 기록은 로그다. 외부 채널이 모두 실패하면 web-backend의 무인증 internal 라우트(`POST /internal/alarms`)로 시스템 알람을 전송하며, web-backend는 이를 수신해 admin WS `system_alarm` 채널로 브로드캐스트한다(§출력 2). notifier는 전송 시도 + 그 결과(2xx/비 2xx)를 로그로 남긴다.

## 의존 도구 · 시스템

| 시스템 | 용도 | 계약 소유자 |
|--------|------|-------------|
| web-backend (REST) | 비상연락처 목록 조회, 임시 CCTV 링크 발급, 시스템 알람 기록 시도, site_url 설정 조회, 카메라 목록 조회 | `docs/spec/interface-web-api.md` |
| recording (REST) | 인시던트 녹화 세그먼트 보호 요청 (two-phase 아카이브의 1단계) | `docs/spec/recording.md` (§HTTP API의 `POST /api/archives/protect`) |
| KakaoTalk 알림톡 API (외부) | 1순위 알림 채널. `X-Api-Key` + `X-Sender-Key` 인증, 사전 등록된 템플릿 기반 | 외부 벤더 |
| NHN Cloud SMS v3.0 (외부) | 2순위 알림 채널. `X-Secret-Key` 인증 | 외부 벤더 |
| SMTP 서버 (선택) | 독립 이메일 채널 + 범용 이메일 발송 API | 외부 인프라 |

환경변수 계약:

| 변수 | 의미 |
|------|------|
| `WEB_BACKEND_URL` | web-backend 베이스 URL (기본 `http://web-backend:8080`) |
| `FRONTEND_URL` | CCTV 링크 조립 시 site_url 설정이 없을 때의 폴백 베이스 |
| `RECORDING_URL` | 녹화 보호 요청 대상 (기본 `http://recording:8080`, 빈 값이면 보호 요청 생략) |
| `KAKAO_ENABLED` | 문자열 `"true"`일 때만 KakaoTalk 채널 시도 |
| `KAKAO_API_URL`, `KAKAO_API_KEY`, `KAKAO_SENDER_KEY`, `KAKAO_TEMPLATE_CODE` | KakaoTalk 접속 정보. URL/KEY 중 하나라도 빈 값이면 해당 채널은 실패로 처리되어 다음 단계로 넘어간다 |
| `SMS_ENABLED` | 문자열 `"true"`일 때만 SMS 채널 시도 |
| `NHN_SMS_APP_KEY`, `NHN_SMS_SECRET_KEY`, `NHN_SMS_SENDER_NO` | NHN SMS 접속 정보. APP_KEY/SECRET_KEY 중 하나라도 빈 값이면 해당 채널은 실패로 처리 |
| `SMTP_HOST`, `SMTP_PORT`, `SMTP_USER`, `SMTP_PASS`, `SMTP_FROM` | 이메일 채널. HOST/FROM 중 하나라도 빈 값이면 이메일 채널 비활성 |

## 입력

### 1. `POST /api/notify` — 위기 이벤트 (주 입력)

- hw-gateway가 MQTT `safety/{siteId}/alert` 페이로드를 그대로 전달한다. **페이로드 필드의 정의·의미는 `docs/spec/interface-mqtt.md`가 소유**하며 이 스펙은 재정의하지 않는다.
- notifier가 강제하는 수용 조건: `siteId`, `deviceId`, `type`, `timestamp` 4개 필드가 모두 비어 있지 않아야 한다. 하나라도 없으면 `400`으로 거절하고 어떤 발송도 하지 않는다.
- `description`이 비면 `"{type} at {siteId}"`, `severity`가 비면 `"unknown"`이 기본값으로 주입된다 — 하위 채널의 템플릿 변수는 절대 빈 값이 되지 않는다.
- `test: true`인 이벤트는 실제 발송 경로를 그대로 타되, 모든 채널의 메시지에 `[테스트]` 표식이 붙는다.

### 2. `POST /api/send-email` — 범용 이메일 발송 (내부 전용)

- Body: `{to, subject, body}` — 셋 다 필수, 누락 시 `400`. 요청 본문 1MB 제한.
- 사설/루프백 IP 대역(Docker 내부망)에서 온 요청만 수용하며, 외부 IP는 `403`으로 거절한다.
- SMTP 미설정 시 `503`.
- `body`는 발송 전 HTML sanitize를 거친다: `<script>`/`<iframe>`은 내용째 제거, `on*` 이벤트 속성 제거, `javascript:` URI 제거, 허용 태그(p, a, br, strong, em, h1~h6, html, head, body) 외의 태그는 껍데기만 제거.

### 3. `GET /healthz` — 헬스체크

- 항상 `200` + `{"status":"ok","service":"notifier"}`.

## 출력 (계약)

1. **즉시 수락** — 유효한 위기 이벤트에 대해 `/api/notify`는 발송을 기다리지 않고 즉시 `200` + `{"status":"accepted", ...}`를 반환한다. 발송 전 과정은 비동기.
2. **연락처별 독립 fallback 체인** — web-backend에서 받아온 각 연락처마다 병렬로, 다음 순서를 보장한다:
   - KakaoTalk 성공 → 해당 연락처 완료.
   - KakaoTalk 실패·비활성(`KAKAO_ENABLED≠true`)·미설정 → SMS 시도.
   - SMS 성공 → 해당 연락처 완료.
   - SMS까지 실패·비활성·미설정 → web-backend `POST /internal/alarms`로 `notification_failure` 타입 시스템 알람 전송을 **시도**한다. 시도 페이로드에는 연락처 식별 정보, siteId/deviceId, 두 채널의 실패 사유가 포함되며, 호출이 실패하거나 비 2xx 응답이면 그 결과가 로그로 남는다. web-backend는 이 라우트에서 알람을 수신해 admin WS `system_alarm` 채널로 브로드캐스트한다(DB 영속은 없음 — 보장 동작은 "수신 + admin 브로드캐스트"). **본 스펙이 보장하는 것은 "전송 시도 + 결과 로그"까지다.**
   - 즉, 외부 채널이 하나도 설정되지 않은 시스템에서도 각 연락처의 전달 실패는 최소한 notifier 로그로 연락처 수만큼 관측된다.
3. **연락처 간 격리** — 한 연락처의 실패가 다른 연락처의 발송에 영향을 주지 않는다 (연락처별 goroutine 병렬).
4. **이메일은 독립 채널** — `notifyEmail=true`이고 이메일 주소가 있는 연락처에는 KakaoTalk/SMS 체인과 무관하게 병렬로 위기 이메일을 발송한다. 이메일 성패는 fallback 체인 진행에 영향을 주지 않으며, 실패는 로그로만 남는다.
5. **CCTV 임시 링크 degraded 허용** — 알림 발송 전에 web-backend에서 임시 링크(`POST /api/links/temp`)를 1회 발급받아 `{site_url}/view/{token}` 형태로 모든 채널 메시지에 포함한다. 베이스 URL은 web-backend의 `site_url` 설정을 우선하고, 조회 실패·빈 값이면 `FRONTEND_URL`로 폴백한다. **링크 발급이 실패해도 알림 발송은 링크 없이 계속된다** — 링크는 알림의 필요조건이 아니다.
6. **녹화 보호 요청** — 알림 dispatch가 끝난 뒤, recording 서비스에 인시던트 세그먼트 보호를 요청한다. 인시던트 ID는 `incident_{siteId}_{UTC시각}` 형식이며, web-backend에서 조회한 **활성(enabled) 카메라 전체의 streamKey**를 대상으로 한다. 이벤트 `timestamp`는 RFC3339 또는 `YYYY-MM-DD hh:mm:ss` 형식으로 파싱 가능해야 하며, 파싱 실패·카메라 0대·recording 미설정 시 보호 요청만 생략되고 알림 결과에는 영향이 없다.
7. **유한 지연** — 모든 외부 HTTP 호출은 응답 헤더 5초 / 전체 10초 타임아웃을 가진다. 따라서 한 연락처의 체인은 유한 시간 내에 반드시 종결된다.
8. **관측 가능성** — 접수, 채널별 성공/실패, degraded 진입, 시스템 알람 기록 시도의 결과, 보호 요청 결과, dispatch 요약(성공 수/채널별 집계)이 모두 로그로 남는다. 어떤 실패도 로그 없이 소멸하지 않는다.

## 핵심 로직 (동작)

1. 위기 이벤트 수신 → 필수 4필드 검증(실패 시 400 종료) → 선택 필드 기본값 주입 → 즉시 200 응답, 이후 비동기 진행.
2. web-backend에서 비상연락처 목록 조회. 조회 실패 또는 0건이면 이 이벤트의 발송은 전면 중단되고 로그만 남는다 (보낼 대상이 없음).
3. 임시 CCTV 링크 1회 발급 시도 → 성공 시 링크 조립, 실패 시 링크 없이 진행 (degraded).
4. 연락처마다 병렬 실행:
   - 이메일 대상이면 이메일을 별도 병렬 발송.
   - KakaoTalk(활성 시) → SMS(활성 시) → 시스템 알람 기록 시도 순서의 순차 fallback. 성공한 첫 채널에서 종료.
5. 전 연락처 완료 대기 후 dispatch 요약 로그.
6. recording에 인시던트 세그먼트 보호 요청 (best-effort).

## 검증 단언 (TDD)

전제: sentinel 스택이 기동 중이고, 단언은 Docker 내부망의 다른 컨테이너에서 notifier(`http://sentinel-notifier:8080` 또는 compose 서비스명 `notifier:8080`)로 직접 주입한다. 관측은 `docker compose logs notifier`로 한다.

- A. **즉시 수락**: 필수 4필드를 갖춘 위기 이벤트를 POST하면 1초 이내에 `200`과 body `"status":"accepted"`가 반환된다.
  ```bash
  curl -s -o /dev/null -w '%{http_code} %{time_total}\n' -X POST http://notifier:8080/api/notify \
    -H 'Content-Type: application/json' \
    -d '{"siteId":"site1","deviceId":"TEST-01","type":"gas_leak","timestamp":"2026-07-02T00:00:00Z","test":true}'
  ```
  → OK: `200`이고 time_total < 1.0. NOK: 그 외.
- B. **불량 입력 거절**: `siteId`(또는 `deviceId`/`type`/`timestamp`) 누락 페이로드 POST → `400` 반환, notifier 로그에 어떤 채널 발송 시도도 나타나지 않는다.
- C. **최후 보루 시도 + 결과 로그**: `KAKAO_ENABLED`/`SMS_ENABLED`가 둘 다 true가 아닌 상태에서 연락처가 N(≥1)명일 때 유효 이벤트 1건 주입 → notifier 로그에 시스템 알람 전송 시도의 결과 기록(성공 또는 `System alarm failed`)이 정확히 N건 남는다 (연락처당 1건). OK/NOK: 알람 시도 결과 로그 건수 = 연락처 수. (web-backend `POST /internal/alarms` 수신 라우트가 무인증 2xx로 응답하므로 각 시도는 성공 로그로 종결되고, 접속 중 admin WS 클라이언트는 `system_alarm` 메시지를 수신한다. 알람의 DB 적재 여부는 단언하지 않는다.)
- D. **링크 degraded**: 임시 링크 발급이 실패하는 상황(web-backend 링크 API 불가)에서 유효 이벤트 주입 → notifier 로그에 degraded 진입이 기록되고, 그럼에도 각 연락처에 대한 채널 시도(또는 시스템 알람)가 관측된다. NOK: 링크 실패로 발송 자체가 중단되는 경우.
- E. **테스트 표식**: `test:true` 이벤트 주입 → 발송되는 SMS/이메일 본문(또는 KakaoTalk 변수)의 설명에 `[테스트]` 표식이 포함된다.
- F. **기본값 주입**: `description`/`severity` 없이 유효 이벤트 주입 → notifier 로그에 fallback 주입이 기록되고, 발송 메시지의 해당 자리들이 빈 문자열이 아니다.
- G. **이메일 API 접근 통제**: 외부(공인) IP에서 `/api/send-email` POST → `403`. 내부망에서 `<script>alert(1)</script>` 포함 body로 POST → 발송된 메일 본문에 `<script>`가 존재하지 않는다.
- H. **헬스체크**: `curl -s http://notifier:8080/healthz` → `200` + `"status":"ok"`.
- I. **녹화 보호 트리거**: 활성 카메라가 1대 이상일 때 유효 이벤트 주입 → notifier 로그에 인시던트 ID(`incident_{siteId}_...`)와 카메라 수를 포함한 보호 요청 수락 기록이 남는다. recording이 내려가 있어도 알림 결과(단언 C 등)는 변하지 않는다.
- J. **연락처 없음 시 종결**: 연락처 0건 상태에서 유효 이벤트 주입 → `200` accepted는 반환되고, notifier 로그에 "대상 없음으로 스킵" 기록이 남으며 어떤 채널 호출도 발생하지 않는다.

## ⚠️ 리뷰 필요 (의도 불확실)

1. **채널 활성 플래그의 기본값이 '꺼짐'** — KakaoTalk/SMS는 `KAKAO_ENABLED`/`SMS_ENABLED`가 문자열 `"true"`일 때만 시도된다. 서비스 가이드(`docs/services/notifier.md`)의 환경변수 표에는 이 두 변수가 없고 "API 키가 빈 값이면 스킵"으로만 기술되어 있다. 운영 배포에서 이 플래그를 설정하지 않으면 API 키가 있어도 두 외부 채널이 전부 비활성화되어 모든 위기 알림이 시스템 알람으로만 남는다. 의도된 안전장치(비용 방지)인지, 문서 누락인지 확인 필요.
2. **연락처 조회 실패/0건 시 시스템 알람 없음** — 연락처 목록 조회가 실패하거나 0건이면 로그 한 줄만 남고 이벤트가 종료된다. web-backend alarms에는 아무 기록도 남지 않아, "silent fail 금지" 원칙과 긴장 관계다 (인시던트 기록 자체는 hw-gateway의 별도 경로 소관). 이 경우에도 시스템 알람을 남기는 것이 의도인지 확인 필요.
3. **응답의 `contactCount`가 항상 0** — `/api/notify` 200 응답 body에 `contactCount: 0`이 고정으로 들어간다 (비동기라 시점상 알 수 없음). 의미 없는 필드를 계약에 남길지, 제거할지 결정 필요.
4. **엔드포인트별 접근 통제 비대칭** — `/api/send-email`은 내부 IP 검사로 보호되지만 `/api/notify`는 아무 통제가 없다. 네트워크 격리(내부망 전용) 전제라면 문제없으나, 한쪽만 방어하는 설계가 의도인지 확인 필요.
5. **녹화 보호 요청이 알림 완료 후 순차 실행** — 보호 요청은 전 연락처의 fallback 체인이 끝난 뒤에 나간다. 외부 API 타임아웃(채널당 최대 10초) 동안 보호 요청이 지연될 수 있다. 보호 대상 구간이 인시던트 이전 시간대이므로 실질 위험은 작아 보이나, 알림과 병렬로 즉시 보내는 것이 의도가 아닌지 확인 필요. 또한 이 동작 전체가 서비스 가이드에 상세 기재되어 있지 않다.
