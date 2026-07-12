# 채널별 테스트 발송 스펙

> 상태: 살아있는 계약 (living spec) · 독자: 스펙 작성자 / 오케스트레이터

## 목적 / 의도

- 비상연락망(수신자·채널)을 설정하는 **관리자**가, 저장해 둔 알림 채널이 실제로 동작하는지를 **채널마다 한 번씩 따로** 확인할 수 있게 한다. 이메일은 이메일대로, SMS는 SMS대로 독립적으로 검증한다.
- 관리자 화면에는 이메일 입력 옆의 "테스트 전송" 버튼과, 연락처(전화번호) 옆의 "테스트 전송" 버튼이 있다. 각 버튼은 관리자가 그 자리에서 입력한 **명시 단일 대상**에게 자기 채널로 **단 1건의 테스트 메시지**만 보내고 그 시도의 성패를 동기적으로 돌려준다. 등록된 비상연락처 전체로 팬아웃하지 않는다.
- 핵심 의도 네 가지:
  1. **채널 독립** — 이메일 테스트가 SMS를 발송하지 않고, SMS 테스트가 이메일을 발송하지 않는다. 두 트리거는 완전히 분리된다.
  2. **요청 시점 설정 반영** — 어떤 채널이 사용 가능한지는 **요청을 처리하는 그 시점에 운영 발송기(notifier)의 현재 실행 config를 조회**해 판정한다(web-backend가 자체적으로 추측하지 않는다). notifier의 config는 배포 시점 env로 고정되므로 config 변경 자체에는 notifier 재시작이 필요하지만, notifier가 재시작된 뒤에는 **web-backend를 재시작하지 않아도** 다음 요청부터 새 판정이 반영된다. 미설정 채널의 테스트는 크래시·무한 대기·거짓 성공 주장 없이 명확한 **"미설정(not_configured)"** 결과를 돌려주고, UI는 이를 표시하거나 버튼을 비활성화하며 안내한다.
  3. **발송 대상 격리(스팸 방지)** — 테스트는 관리자가 명시 입력한 단일 대상에게만 도달한다. contactId·등록 연락처로의 팬아웃은 없다. 채널별 레이트리밋으로 반복 발송을 억제한다.
  4. **정직한 성패 보고** — 설정된 채널의 테스트는 운영 채널 발송기와 **동일한 자격증명·전송 구현**으로 발송을 시도하고, 그 시도의 성공/실패를 그대로 보고한다(설정되어 있다는 이유만으로 성공을 주장하지 않는다).
- **지원 채널 집합은 정확히 `{email, sms}`이며, 그 밖의 channel(예: KakaoTalk)을 지정한 테스트 요청은 `400`으로 거절된다.** KakaoTalk 테스트 발송은 이 단위의 범위 밖(연기)이다.

## 언어 · 런타임

- 관리자 대면 계약 표면은 중앙 REST API(web-backend, Go, JWT admin 인증)와 그 위의 관리자 UI 어포던스(web-frontend, React) 두 층으로 구성된다(식별용 — 구현 지시가 아니다).
- 실제 채널 발송은 운영 채널 발송기(notifier)와 **동일한 자격증명·전송 구현**을 통해 이루어진다. 단, 위기 알림이 쓰는 **fallback 체인(KakaoTalk→SMS→시스템알람)과 연락처 팬아웃은 우회**하고, 지정된 단일 채널·단일 대상만 시도한다. 테스트라 해서 별도 목(mock) 경로를 타지 않는다.

## 의존 도구 · 시스템

| 시스템 | 용도 | 계약 소유자 |
|--------|------|-------------|
| 관리자 REST API (web-backend) | 테스트 발송 트리거와 채널 사용가능 상태(status) 읽기의 호스트이자 admin 인증 경계 | `docs/spec/interface-web-api.md` (본 스펙의 "API 계약 델타" 참조) |
| 운영 이메일 발송 경로 | 설정 시 실제 이메일 1건 발송 시도 | 시스템 이메일 채널 (SMTP 등) |
| 운영 SMS 발송 경로 | 설정 시 실제 SMS 1건 발송 시도 | 시스템 SMS 채널 (외부 벤더) |
| 관리자 UI (web-frontend) | 이메일/연락처 옆 "테스트 전송" 버튼 + 미설정 채널 표시·비활성·안내 | `docs/services/web-frontend.md` |
| notifier 실행 config (채널 상태 소스) | 채널별 자격증명·활성 여부의 판정 출처. status 판정과 실제 발송이 **동일한 소스(notifier)** 를 참조 — status 소스 = 발송 소스 | `docs/spec/notifier.md` (본 스펙의 "API 계약 델타" 참조) |

- 채널 집합은 이 단위에서 정확히 `{email, sms}` 두 종이다. KakaoTalk은 이 집합에 포함되지 않는다.

## 입력

### 1. 채널 사용가능 상태 읽기 (status) — 관리자

- 입력 없음(admin 인증만). in-scope 채널 각각의 사용가능 여부를 조회한다.

### 2. 테스트 이메일 트리거 — 관리자

- 관리자가 그 자리에서 입력한 **대상 이메일 주소 1개**(발송 목적지). 주소가 없거나 형식이 유효하지 않으면 발송 없이 거절(400). 등록 연락처로 팬아웃하지 않는다.

### 3. 테스트 SMS 트리거 — 관리자

- 관리자가 그 자리에서 입력한 **대상 전화번호 1개**(명시 단일 값). 값이 없거나 전화번호 형식(`01[016789]-\d{3,4}-\d{4}`)이 아니면 발송 없이 거절(400). contactId·등록 연락처로 팬아웃하지 않는다.

### 4. 레이트리밋 (공통)

- 채널별로 **분당 1건**을 초과하는 테스트 발송 요청은 `429`로 거절되며 발송 시도 0건이다(반복 발송 억제).

## 출력 (계약)

1. **채널 상태 계약** — status 읽기는 in-scope 채널(`email`, `sms`) **각각**에 대해 `usable`(boolean)과 미사용 시 사람이 읽을 수 있는 사유(reason)를 제공한다. `usable` 값은 web-backend의 자체 추측이 아니라 **notifier의 현재 실행 config 조회 결과를 그대로 투사**한 것이다. 응답의 채널 집합에는 **KakaoTalk이 존재하지 않는다**.
2. **usable 판정 규칙 (status·발송 공유)** — `usable`은 "해당 채널이 실제 발송을 **시도할** 조건이 충족됨"을 뜻한다. SMS는 `SMS_ENABLED=="true"` **그리고** 자격증명 존재일 때, 이메일은 `SMTP_HOST`와 `SMTP_FROM`이 **모두** 존재일 때 `usable=true`다. `ENABLED`가 꺼져 있으면 자격증명이 있어도 `usable=false`다. status 판정과 실제 발송의 설정/미설정 분기는 **동일한 판정 함수**를 공유한다(둘이 어긋나지 않음).
3. **테스트 결과 어휘(3종)** — 테스트 이메일·테스트 SMS 트리거의 결과는 정확히 다음 3종 중 하나이며, outcome은 **실제 전송 시도 관측 결과와 일치**한다:
   - `sent` — 실제 발송 경로로 발송을 시도했고 채널/전송이 수락함.
   - `failed`(+사유) — 실제 발송 경로로 시도했으나 전송/공급자 오류로 실패.
   - `not_configured` — 해당 채널이 요청 시점 판정상 사용 불가(§출력 2). **발송을 시도하지 않는다.**
4. **not_configured ↔ failed 경계** — "설정됨" 판정은 **필수 자격 값의 존재 여부로만** 결정한다. 필수 값이 존재하면 채널은 설정된 것이고, 그 상태에서 접속/전송이 실패하면 `failed`다(`not_configured`가 아니다). `not_configured`는 오직 필수 자격 값이 부재할 때만 나온다.
5. **채널 독립** — 테스트 이메일 트리거는 이메일 발송 경로만 시도한다(SMS 발송 0건). 테스트 SMS 트리거는 SMS 발송 경로만 시도한다(이메일 발송 0건). 하나의 트리거가 다른 채널을 부수적으로 발동시키지 않는다.
6. **발송 대상 격리 (팬아웃 없음)** — 테스트 발송은 관리자가 지정한 **단일 대상**에게만 도달한다. 등록된 비상연락처(contactId 포함)로의 팬아웃은 **0건**이다. 운영 발송기의 자격증명·전송 구현은 공유하되 fallback 체인과 연락처 팬아웃은 우회한다.
7. **요청 시점 판정 (web-backend 재시작 불요)** — 채널 `usable` 판정과 테스트의 미설정/설정 분기는 모두 **요청 처리 시점에 notifier 실행 config를 조회**해 산출한다. notifier config는 배포 시점 env이므로 config 변경에는 notifier 재시작이 필요하지만, **notifier 재시작 후에는 web-backend 재시작 없이** 다음 요청부터 반영된다. status가 참조하는 소스와 실제 발송이 참조하는 소스는 **동일(notifier)** 하다.
8. **미설정 시 안전 종결(크래시·무한 대기 금지)** — 미설정 채널로 테스트를 요청하면 유한 시간 내에 `not_configured`로 종결한다. 어떤 경우에도 요청이 크래시하거나 무한 대기하거나 조용히 성공을 주장하지 않는다.
9. **유한 지연 (타임아웃 상한 수치화)** — 설정된 채널이라도 대상 공급자/서버가 무응답이면 테스트 요청은 notifier의 채널 타임아웃 예산(`docs/spec/notifier.md` §출력 7의 채널당 소요 상한 **≤12초**) 내에 `failed`로 종결되며 무한 대기하지 않는다.
10. **레이트리밋** — 채널별로 분당 1건을 초과하는 테스트 요청은 `429`로 거절되고 발송 시도 0건이다.
11. **관리자 전용 권한** — status 읽기·테스트 이메일·테스트 SMS 세 표면 모두 **연락처 CUD(쓰기: POST/PUT/DELETE `/api/contacts`)와 동일한 admin 권한**을 요구한다(연락처 목록 `GET /api/contacts`의 user 권한과 다름 — 혼동 주의). 비-admin(user) 요청은 `403`, 무인증 요청은 `401`로 거절되며 어떤 발송도 하지 않는다.
12. **KakaoTalk 부재 (지원 채널 집합)** — 지원 채널 집합은 정확히 `{email, sms}`다. status 채널 집합에 KakaoTalk이 없고, `channel ∉ {email, sms}`(예: KakaoTalk)을 지정한 테스트 요청은 `400`으로 거절된다(발송 0건).
13. **UI 어포던스** — 관리자 화면은 이메일 필드 옆과 연락처(전화번호) 옆에 각각 "테스트 전송" 버튼을 제공한다. 채널이 미설정(status `usable=false`)이면 해당 버튼은 비활성화되거나 눌렀을 때 `not_configured`를 사용자에게 안내한다(성공한 것처럼 보이게 하지 않는다).

## 핵심 로직 (동작)

1. status 요청 → 요청 시점에 notifier 실행 config를 조회해 `email`·`sms` 각각의 `usable`/`reason`을 산출·반환(§출력 1·2·7). web-backend는 notifier 결과를 그대로 투사하고 자체 판정하지 않는다. KakaoTalk은 산출·노출 대상이 아님(§출력 12).
2. 테스트 이메일 요청 → admin 권한·레이트리밋 확인(§출력 10·11) → 대상 주소 검증(실패 시 400) → 요청 시점 판정으로 이메일 채널 사용가능 판정(§출력 2·7) → 미설정(필수 자격 부재)이면 발송 없이 `not_configured` 반환(§출력 4·8) → 설정이면 운영 이메일 발송기의 자격증명·전송 구현으로 지정 대상에게 1건만 시도(fallback·연락처 팬아웃 우회, §출력 6)하고 그 성패를 `sent`/`failed`로 반환(§출력 3·4). SMS 경로는 건드리지 않음(§출력 5).
3. 테스트 SMS 요청 → admin 권한·레이트리밋 확인 → 대상 전화번호 검증(실패 시 400) → 요청 시점 판정으로 SMS 채널 사용가능 판정 → 미설정이면 발송 없이 `not_configured` → 설정이면 운영 SMS 발송기로 지정 대상에게 1건만 시도(fallback·팬아웃 우회)하고 성패 반환. 이메일 경로는 건드리지 않음(§출력 5).
4. 세 표면 모두 진입 시 admin 권한을 강제한다(§출력 11).

## API 계약 델타 (선언 — SSOT 미편집)

> 이 단위는 web-api 접면과 notifier internal 접면에 아래 표면을 **추가**한다. 접면 계약의 정본(SSOT)은 `docs/spec/interface-web-api.md`(web-api)와 `docs/spec/notifier.md`(notifier internal)이며, **본 스펙은 그 문서들을 편집하지 않고 추가될 계약의 윤곽만 델타로 선언**한다(정본 반영은 오케스트레이터/구현 주체의 몫). 경로명은 대표 형태이며 최종 명명은 각 SSOT가 소유한다.

**web-backend (admin — `interface-web-api.md` SSOT):**

- **채널 사용가능 상태 읽기(admin)** — in-scope 채널별 `{channel, usable, reason}`를 반환. `channel ∈ {email, sms}`. KakaoTalk 미포함. web-backend는 **notifier 조회 결과를 그대로 투사**하며 자체 판정하지 않는다. (대표: `GET /api/notifications/channels`)
- **채널별 테스트 발송(admin) — 단일 엔드포인트 확정** — `POST /api/notifications/test {channel, target}` **하나로 확정**(채널별 분리 엔드포인트 옵션은 삭제). `channel ∈ {email, sms}`, `target`은 관리자가 입력한 명시 단일 값(이메일 주소/전화번호; contactId 아님). notifier의 단건 동기 발송 경로를 **프록시**하고 `{outcome}` 반환. `outcome ∈ {sent, failed, not_configured}`; `failed`는 사유 동반. `channel ∉ {email, sms}`(예: KakaoTalk) → `400`(단언 G). 채널별 분당 1건 초과 → `429`, 발송 0건.
- **권한**: 위 두 표면 전부 admin 전용 — **연락처 CUD(POST/PUT/DELETE `/api/contacts`)와 동일 권한**이며 `GET /api/contacts`의 user 권한과 구분. 비-admin `403`, 무인증 `401`.

**notifier internal (`notifier.md` SSOT — 반영 필요 노트, 오케스트레이터가 머지 시 정본 반영):**

- **채널 상태 조회(신규)** — `GET /internal/channel-status → {email:{usable,reason}, sms:{usable,reason}}`. 각 채널이 자기 실행 env(SMS: `SMS_ENABLED=="true"`+자격증명, email: `SMTP_HOST`+`SMTP_FROM`)로 `usable`을 판정한다. web-backend status가 이를 그대로 투사한다. **status 소스 = 발송 소스 = notifier 동일.**
- **단건 동기 테스트 발송(신규)** — `POST /internal/test-send {channel, target}`. 지정한 **채널 하나만** 지정 대상에게 시도하고(위기 경로의 fallback 체인·연락처 팬아웃을 **우회**), 동기적으로 `sent`/`failed`/`not_configured`를 반환한다. 운영 발송과 **동일한 자격증명·전송 구현**을 재사용하되, 설정/미설정 판정은 §출력 2 규칙을, 채널 타임아웃 예산은 §출력 9(notifier §출력 7의 ≤12s)를 공유한다.

## 검증 단언 (TDD)

전제: sentinel 스택 기동 중. admin JWT(`ADMIN_TOKEN`)와 user JWT(`USER_TOKEN`)를 확보한다. 테스트는 web-backend(`http://web-backend:8080`, 프록시 경유 시 `/api/...`)로 요청한다.

**발송 시도 관측점 (픽스처 계약)**: 채널별 발송-시도는 **mock 공급자의 수신 카운트** 또는 **지정 로그 패턴**으로 결정적으로 관측한다(이 관측점을 픽스처가 고정한다). "발송 시도"는 해당 채널 발송 경로에 도달한 지정 단일 대상 1건의 전송 시도를 뜻한다.

**Vacuity 주의(정직성)**: `sent`/`failed`/`timeout` 분기(단언 A·H·I 및 C·D·E·L의 "설정된 채널" 방향)를 non-vacuous하게 판정하려면 실제(또는 mock) SMTP·실패 주입 공급자·무응답(지연) 공급자 구성 픽스처(예: notifier `SMTP_HOST=mock-smtp` + 실패·지연 주입)가 있어야 한다. 그 픽스처가 없으면 해당 분기 단언은 **공허**이므로 OK로 위장하지 말고 **SKIP(부적절, no-config/no-gateway)** 으로 선언한다(아래 "검증 스킵 선언"). 반면 `not_configured` 분기(단언 B, C·D의 미설정 방향, F 권한, G KakaoTalk, J 입력검증, K 채널 집합/필드, M 레이트리밋)는 기본 스택(채널 미설정)에서 non-vacuous이므로 픽스처 없이 즉시 판정한다.

- **A (핵심 · 설정 픽스처 필요, 없으면 SKIP). 설정된 이메일 테스트 → 성공 + 실제 발송 시도**: 이메일 채널이 설정된 상태에서 특정 주소로 테스트 이메일을 요청하면 응답 `outcome`이 `sent`이고, 이메일 발송 경로에 정확히 1건의 발송 시도가 관측된다. 같은 요청으로 SMS 발송 시도는 0건이다. OK: `sent` + 이메일 시도 1건 + SMS 시도 0건. NOK: 그 외. (mock SMTP 픽스처 없으면 SKIP.)
- **B (핵심 · non-vacuous). 미설정 이메일 테스트 → not_configured(무발송·무크래시)**: 이메일 채널이 설정되지 않은 상태에서 테스트 이메일을 요청하면 응답 `outcome`이 `not_configured`이고(성공 주장 없음), 이메일 발송 시도 0건이며, 요청은 유한 시간 내에 정상 응답으로 종결된다(크래시·타임아웃 무한대기 없음). OK: `not_configured` + 발송 0건 + 유한 응답. NOK: `sent` 주장·발송 발생·크래시·무응답.
- **C (핵심 · 미설정 방향 non-vacuous / 설정 방향 픽스처 필요). SMS 테스트 미설정/설정 분기**: SMS 채널 미설정 상태에서 테스트 SMS 요청 → `not_configured` + SMS 발송 0건 + 크래시 없음. 이어서 SMS 채널을 설정한 뒤 동일 요청 → SMS 발송 경로에 발송 시도 1건이 관측되고 응답은 `sent` 또는 (공급자 오류 시) `failed`다. OK: 미설정=not_configured·무발송, 설정=발송 시도 1건. NOK: 미설정인데 발송 시도 발생·크래시, 또는 설정인데 발송 시도 없음. (설정 방향은 SMS mock 공급자 픽스처 없으면 SKIP; 미설정 방향은 즉시 판정.)
- **D (핵심 · 미설정→false non-vacuous / 설정→true 픽스처 필요). 요청 시점 notifier 조회(web-backend 재시작 불요)**: 채널 usable 판정은 요청 시점에 **notifier의 현재 실행 config를 조회**해 산출하며 web-backend는 자체 추측하지 않는다. 이메일 채널을 미설정 config의 notifier로 두고 status를 읽으면 `email.usable=false`. notifier config를 설정으로 바꿔 **notifier만 재시작**(web-backend는 재시작하지 않음)한 뒤 곧바로 status를 다시 읽으면 `email.usable=true`로 바뀌고, 테스트 트리거의 `not_configured`↔`sent`(또는 `failed`) 분기도 동일하게 뒤바뀐다. status 소스와 발송 소스가 동일 notifier임을 확인한다. OK: notifier config 변경이 **web-backend 재시작 없이** 다음 요청부터 usability/분기에 반영. NOK: web-backend가 자체 판정하거나, web-backend 재시작을 해야만 반영되거나, 프로세스 시작 시점 값에 고정. (config 전환 픽스처: 설정/미설정 두 notifier config + notifier 재기동 — 미설정→`false` 방향은 non-vacuous, 설정→`true` 방향은 픽스처 없으면 SKIP.)
- **E (핵심 · 설정 픽스처 필요, 없으면 SKIP). 채널 독립(교차 발동 없음)**: 이메일·SMS 채널이 설정된 상태에서 테스트 이메일을 1회 요청하면 이메일 발송 경로에만 시도가 관측되고 SMS 발송 시도는 0건이다. 테스트 SMS를 1회 요청하면 그 반대다. OK: 각 트리거가 자기 채널만 발동. NOK: 한 트리거가 다른 채널 발송을 유발. (설정 픽스처 없으면 SKIP.)
- **F (핵심 · non-vacuous). 관리자 전용 권한**: status·테스트 이메일·테스트 SMS 세 표면 각각에 대해 user JWT로 요청하면 `403`, 무인증(토큰 없음)으로 요청하면 `401`이며, 어느 경우에도 발송 시도가 0건이다. admin JWT로는 권한 관문을 통과해 정상 처리된다. OK: 비-admin 403 / 무인증 401 / 발송 0건. NOK: 비-admin 또는 무인증이 발송을 유발하거나 2xx로 처리됨.
- **G (핵심 · non-vacuous). 지원 채널 집합 = 정확히 {email, sms}**: status 응답의 채널 집합은 정확히 `email`·`sms`뿐이다(KakaoTalk 없음). `channel ∉ {email, sms}`(예: KakaoTalk)을 지정한 테스트 발송 요청은 `400`으로 거절된다(발송 시도 0건). OK: status 채널 = {email, sms} + 그 밖 channel 테스트 400·무발송. NOK: status에 다른 채널 노출되거나 그 밖 channel 테스트가 발송을 시도.
- **H (일반 · 실패 공급자 픽스처 필요, 없으면 SKIP). 설정됐어도 실패는 실패로 보고**: 이메일(또는 SMS) 채널이 설정되어 있으나(필수 자격 값 존재) 발송이 공급자/전송 오류로 실패하도록 구성한 뒤 테스트를 요청하면 응답 `outcome`이 `failed`(+사유)이며 `sent`도 `not_configured`도 아니다(설정됨→실패는 `failed`, §출력 4). OK: 실패 시 `failed`. NOK: 실제 실패인데 `sent`를 주장하거나 `not_configured`로 강등. (실패 주입 공급자 픽스처 없으면 SKIP.)
- **I (일반 · 무응답 공급자 픽스처 필요, 없으면 SKIP). 유한 지연(타임아웃 상한 수치화)**: 설정된 채널의 대상 공급자가 무응답이 되도록 구성한 뒤 테스트를 요청하면, 요청은 notifier §출력 7의 채널당 소요 상한(**≤12초**) 내 유한 시간에 `failed`로 종결된다(무한 대기 없음). OK: ≤12s 내 `failed` 종결. NOK: 12s 상한을 넘겨 종결·무한 대기. (지연 주입 공급자 픽스처 없으면 SKIP.)
- **J (일반 · non-vacuous). 입력 검증**: 대상 주소가 없거나 형식이 유효하지 않은 테스트 이메일 요청은 발송 없이 `400`. 대상 전화번호가 없거나 전화번호 형식이 아닌 테스트 SMS 요청은 발송 없이 `400`. OK: 잘못된 입력 → 400 + 발송 0건. NOK: 잘못된 입력으로 발송이 발생.
- **K (일반 · non-vacuous / usable 규칙 세부는 픽스처 필요). status 채널 계약 + usable 판정 규칙**: status 응답은 `email`·`sms` 각각에 대해 `usable`(boolean)과 미사용 시 `reason`을 포함하고, 이 두 채널만 노출한다(G와 정합). `usable`은 규칙(§출력 2)을 따른다: SMS는 `SMS_ENABLED=="true"`+자격증명, email은 `SMTP_HOST`+`SMTP_FROM`. `SMS_ENABLED`가 꺼진 채 자격증명만 있는 조합은 `usable=false`여야 한다(status와 발송이 같은 판정 함수). OK: 두 채널 각각 usable/reason 제공 + 그 외 채널 없음 + 규칙 준수. NOK: 채널 누락·과잉 노출·usable 필드 부재, 또는 ENABLED 꺼짐+자격증명 조합에서 usable=true. (채널 집합·필드 존재는 즉시 판정; "ENABLED 꺼짐+자격증명 존재" 세부는 해당 config 픽스처 없으면 그 세부만 SKIP.)
- **L (핵심 · 설정 픽스처 필요, 없으면 SKIP). 발송 대상 격리(팬아웃 0)**: 채널이 설정되고 등록 비상연락처가 N(≥1)명 있는 상태에서 지정 단일 대상으로 테스트를 요청하면, 발송 시도는 **지정 대상 1건뿐**이고 등록 연락처(contactId)로의 발송 시도는 **0건**이다. OK: 지정 대상 1건 + 등록 연락처 팬아웃 0건. NOK: 등록 연락처로 팬아웃하거나 지정 대상 외로 발송. (설정 + 등록 연락처 픽스처 없으면 SKIP.)
- **M (일반 · non-vacuous). 레이트리밋(채널별 분당 1건)**: 한 채널로 분당 두 번째 테스트 발송 요청을 보내면 `429`로 거절되고 그 요청의 발송 시도는 0건이다. OK: 초과 요청 `429` + 발송 0건. NOK: 분당 1건 초과가 발송을 유발하거나 429가 아님.

## 검증 스킵 선언

- **발송 성패 분기(sent/failed/timeout)의 픽스처 부재 시 SKIP (공허 방지)** — 사유: 단언 A·H·I 및 C·D·E·L의 "설정된 채널" 방향은 실제/mock SMTP·SMS 공급자, 실패 주입 공급자, 무응답(지연) 공급자와 발송-시도 관측 카운트/로그 픽스처가 있어야 non-vacuous하다. 해당 픽스처(테스트 SMTP 컨테이너 또는 notifier mock 공급자 모드, 예: `SMTP_HOST=mock-smtp` + 실패·지연 주입)가 없으면 그 분기 단언은 공허하므로 **SKIP(부적절, no-config/no-gateway)** 으로 선언하고 OK로 위장하지 않는다. `not_configured` 분기(B, C·D의 미설정 방향, F, G, J, K 채널 집합, M)는 기본 스택에서 non-vacuous하므로 즉시 판정한다. · 중요도: 핵심(정직성) · 해소 조건: mock 공급자 모드 / 테스트 SMTP 컨테이너 픽스처 확보 시 SKIP 해제 후 즉시 판정.
- **SMS 벤더 배관 정합** — 사유: SMS 발송 벤더의 구체 엔드포인트·인증 헤더 규격은 notifier 스펙과 공유되며 벤더 확정 전 잠정적이다. 본 단위는 SMS를 **추상 발송 경로**(설정 시 전화번호 대상 1건 발송 시도 → 성공/실패 판정)로만 검증하고, 특정 벤더 요청 스키마 정합은 지금 검증하지 않는다. · 중요도: 일반 · 해소 조건: SMS 벤더 스펙 확정 시 구체 계약·단언 추가.
