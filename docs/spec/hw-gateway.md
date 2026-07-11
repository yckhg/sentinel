# hw-gateway 스펙

> 상태: 살아있는 계약 (living spec) · 독자: 스펙 작성자

## 목적 / 의도

hw-gateway는 현장 하드웨어(MQTT)와 소프트웨어 서비스(HTTP) 사이의 **유일한 변환 접점**이다.

- 하드웨어가 MQTT로 보내는 위급 알림(alert)·생존 신호(heartbeat)·후보 탐지(candidate)·해소 통지(resolved)를 수신하여, 소프트웨어 측(notifier, web-backend)에 HTTP로 전달한다.
- 소프트웨어 측에서 온 명령(재시작, 웹 해소, 테스트 알림)을 MQTT 발행으로 변환하여 하드웨어에 전달한다.
- 장비별 생존(alive/dead) 상태와 경보 상태를 in-memory로 유지하고 조회 API로 노출한다. 이 스토어는 보존 상한·TTL로 **유한하게** 유지되어 무한 증가하지 않는다("장비 스토어 보존 상한·eviction" 계약 참조).

의도적으로 **상태 비저장(휘발성)** 이다. 영속 저장은 하지 않으며, 장비의 영속 등록은 web-backend에 위임한다(best-effort 통지).

## 언어 · 런타임

- Go (단일 바이너리, 단일 프로세스).
- Docker 컨테이너로 실행되며 내부 포트 8080에서 HTTP를 서비스한다. 외부 포트 노출 없음.

## 의존 도구 · 시스템

| 의존 대상 | 방식 | 필수 여부 |
|-----------|------|-----------|
| MQTT 브로커 (mosquitto) | subscribe + publish, client ID `sentinel-hw-gateway`(고정), **persistent session (clean session=false)** — 설계자 승인(mosquitto 세션 지속성 설정 필요), **짧은 keep-alive/능동 PING(≤5s 권장 — healthz 단절 감지를 발행 타임아웃 5초와 정합시키기 위함, ⚠ 배포 시 확정)**, 자동 재연결(지수 백오프 최대 60s) | 필수 — 브로커가 죽어도 HTTP 서버는 기동·응답을 유지(hang 없음)하되, `/healthz`는 미연결/미구독을 degraded(503)로 표면화한다("헬스체크(healthz) 계약" 참조) |
| notifier | outbound HTTP (`POST /api/notify`) | alert 전달 대상 |
| web-backend | outbound HTTP (`POST /api/incidents`, `POST /api/devices/seen`, `POST /api/incidents/{id}/resolve-from-sensor`) | alert 기록·장비 등록·센서 해소 전달 대상 |

환경 변수 계약:

| 변수 | 기본값 | 의미 |
|------|--------|------|
| `MQTT_BROKER_URL` | `tcp://mosquitto:1883` | 브로커 주소 |
| `NOTIFIER_URL` | `http://notifier:8080` | notifier 베이스 URL |
| `WEB_BACKEND_URL` | `http://web-backend:8080` | web-backend 베이스 URL |
| `HEARTBEAT_TIMEOUT_SEC` | `30` | 이 시간 초과 미수신 시 장비 dead 판정 (양의 정수만 유효, 그 외는 기본값) |
| `EQUIPMENT_MAX_DEVICES` | `1000` | in-memory 장비 스토어 보존 상한 (양의 정수만 유효, 그 외는 기본값). 초과 시 least-recently-seen 장비부터 eviction |
| `EQUIPMENT_EVICT_TTL_SEC` | `86400` | 마지막 수신(heartbeat/alert/candidate 중 최신) 이후 이 시간 초과 시 장비를 스토어에서 **선택적 청소**(제거)하는 값 (양의 정수만 유효, 그 외는 기본값). **기동 불변식:** `EQUIPMENT_EVICT_TTL_SEC > HEARTBEAT_TIMEOUT_SEC`를 만족해야 dead 장비가 제거 전까지 일정 기간 조회 가능하다. 위반(이하) 시 기본값 `86400`으로 강제 대체하고 경고 로그를 남긴다(⚠ 대안: 기동 거부 — 배포 정책으로 확정). 무한 증가 방지는 이 TTL이 아니라 상한 eviction이 보장한다(§장비 스토어 보존 상한·eviction) |

## 입력

### MQTT 구독 (토픽·QoS·retain·페이로드 스키마의 소유자는 `docs/spec/interface-mqtt.md` — 여기서 재정의하지 않음)

| 토픽 | 의미 |
|------|------|
| `safety/+/alert` | 위급 알림 |
| `safety/+/heartbeat` | 장비 생존 신호 |
| `safety/+/alert/resolved` | 위급 해소 통지 (양방향 — 자기 발행 echo 포함) |
| `safety/+/event/candidate` | threshold 미달 위기 키워드 탐지 (참고용) |

### Inbound HTTP (요청/응답 스키마의 소유자는 `docs/spec/interface-web-api.md` 계약 15)

| 엔드포인트 | 의미 |
|-----------|------|
| `GET /healthz` | 헬스체크 |
| `GET /api/equipment/status` | in-memory 장비 상태 조회 |
| `POST /api/restart` | 재시작 명령 → MQTT 발행 |
| `POST /api/test-alert` | 테스트 alert → MQTT 발행 (실제 alert 경로로 순환 유입됨) |
| `POST /api/alert/resolved` | 웹 발 해소 통지 → MQTT 발행 |

## 출력 (계약)

### MQTT 발행 (토픽·QoS·retain·페이로드 스키마 소유자: `docs/spec/interface-mqtt.md`)

- `safety/{siteId}/cmd/restart` — 발행 시각(timestamp)은 서버가 UTC로 채운다.
- `safety/{siteId}/alert/resolved` — `resolvedAt` 누락 시 서버 UTC 현재 시각, `resolvedBy.kind` 누락 시 `"web"`으로 보정 후 발행.
- `safety/{siteId}/alert` — 테스트 알림 전용(`test: true`). 페이로드는 interface-mqtt의 alert 계약을 따르며, 테스트 alert 스키마의 상세 정의도 그 문서 소유다.

### Outbound HTTP (web-backend 측 계약의 소유자: `docs/spec/interface-web-api.md` 계약 13 · notifier 측 계약: notifier 스펙)

| 호출 | 트리거 | 신뢰성 계약 |
|------|--------|-------------|
| notifier `POST /api/notify` | alert 수신 | 타임아웃 10초, **재시도 없음**, 실패는 로그만 |
| web-backend `POST /api/incidents` | alert 수신 (notifier 호출과 병렬) | 시도당 타임아웃 10초, **transport 에러(연결 실패·타임아웃 등) 및 HTTP 5xx**에 대해 최대 3회 재시도 (지수 백오프 1s 기점 ×2, ±25% jitter). **4xx는 클라이언트 오류로 재시도하지 않는다.** 2xx(생성 201 또는 dedup 200)에서만 성공으로 판정하며, 페이로드에 `alertId`를 포함해 web-backend DB dedup을 발화시킨다 |
| web-backend `POST /api/devices/seen` | heartbeat / alert / candidate 수신 시마다 | best-effort — 타임아웃 5초, 재시도 없음, 실패해도 본 처리 계속 |
| web-backend `POST /api/incidents/{id}/resolve-from-sensor` | `alert/resolved` 수신 + 해소 주체가 sensor_button | 타임아웃 5초, 재시도 없음. 수신 페이로드를 그대로 전달 |

### 장비 상태 조회 응답

`GET /api/equipment/status`는 알려진 모든 장비의 배열 `[{deviceId, siteId, alive, lastHeartbeat, alertState}]`을 반환한다. `lastHeartbeat`는 **서버 수신 시각**(UTC, RFC 3339)이며 장비가 보낸 timestamp가 아니다. `alertState`는 `"none" | "active"`.

## 핵심 로직 (동작)

### 헬스체크(healthz) 계약

`GET /healthz`는 hw-gateway가 **핵심 기능(현장 MQTT 수신)을 실제로 수행할 수 있는지**를 반영한다. HTTP 서버가 살아 있다는 것만으로 healthy를 반환하지 않는다 — 경보 유실 상태가 healthy로 은폐되지 않는 것이 계약이다.

- **"구독 성립"의 정의:** 어떤 토픽이 "구독 성립" 상태라 함은, 브로커가 해당 SUBSCRIBE에 대해 **SUBACK으로 granted QoS(0x80/실패 코드가 아님)**를 반환하여 현재 유효한 구독으로 유지되고 있음을 뜻한다. 연결이 끊기면 모든 구독은 성립 해제로 간주하고, 재연결 후 재구독 SUBACK을 다시 받아야 성립으로 복귀한다.
- **경보성 필수 3토픽만 healthy 판정에 사용:** healthy 판정의 필수 구독은 **경보 안전에 직접 관여하는 3토픽 — `safety/+/alert`, `safety/+/heartbeat`, `safety/+/alert/resolved`** 로 좁힌다. `safety/+/event/candidate`는 여전히 구독하지만 **QoS 0·유실 허용 참고 채널**이므로 healthy 판정에서 **제외**한다(candidate 구독 미성립만으로 degraded로 떨어뜨리지 않는다).
- **healthy(`200`, 본문에 `"status":"ok"`):** MQTT 브로커에 **연결**되어 있고, 경보성 필수 3토픽이 **모두 현재 구독 성립** 상태일 때에만.
- **degraded(`503`, 본문에 `"status":"degraded"`):** 브로커 미연결(최초 연결 전 또는 단절 후 재연결 진행 중)이거나, 경보성 필수 3토픽 중 하나라도 구독이 성립하지 않은 동안. 즉 gateway가 경보(alert/heartbeat/resolved)를 수신할 수 없는 상태는 항상 unhealthy로 표면화된다.
- **응답 상한 ≤ 1s (hang 금지):** `/healthz`는 브로커 상태와 무관하게 **1초 이내에 응답**한다 — 내부 상태 플래그만 읽고 즉시 반환하며 브로커 단절 중에도 블로킹하지 않는다. (이 상한은 발행 타임아웃 5초와 명확히 구분된다 — healthz는 네트워크 왕복이 아니라 in-memory 연결/구독 플래그 조회다.)
- **단절 감지 지연(계약):** healthz가 반영하는 연결 상태는 MQTT 클라이언트가 단절을 **인지한 시점** 기준이다. 브로커가 조용히 사라진 경우(가동 중 프로세스 다운) 클라이언트의 단절 인지는 **keep-alive/PING 경계 이내**로 유한하게 지연될 수 있다. 발행 경로(WaitTimeout 5초)와 시점을 맞추기 위해, gateway는 **짧은 keep-alive 또는 능동 PING(권장 ≤ 5초)**으로 단절을 신속 감지하도록 구성한다 — ⚠(keep-alive 값은 브로커 부하와의 트레이드오프로 배포 시 확정; interface-mqtt의 60s 권장은 H/W 디바이스 대상이고 gateway는 별도 튜닝 가능). 이 구성 전까지 healthz의 degraded 전이는 "즉시"가 아니라 "keep-alive/PING 경계 이내"로 이해한다.
- 재연결·재구독이 자동 성립하면 별도 조치 없이 degraded→healthy로 회복된다.
- (참고: 이 계약은 조회성 엔드포인트 `GET /api/equipment/status`에는 적용되지 않는다. 후자는 브로커 상태와 무관하게 `200`으로 현재 in-memory 상태를 반환한다.)

### alert 처리

1. JSON 파싱 실패 또는 필수 필드(`deviceId`, `siteId`, `type`, `timestamp`) 누락 시 메시지를 무시하고 경고 로그만 남긴다 — 어떤 forward도 발생하지 않는다.
2. **중복 제거:** `alertId`가 있으면 in-memory 캐시로 dedup — 동일 `alertId` 재수신 시 forward 없이 무시. 캐시 항목은 24시간 후 청소(1시간 주기), 프로세스 재시작 시 초기화. `alertId` 누락 시 dedup 없이 처리 계속. **캐시 등록은 web-backend forward가 2xx로 성공한 이후에만 수행한다** — forward가 최종 실패(전송 오류/5xx 재시도 소진)하면 `alertId`를 등록하지 않아 펌웨어 재전송으로 유실을 복구할 수 있다.
3. **siteId 일관성:** 페이로드의 `siteId`는 토픽 경로의 `{siteId}`로 항상 덮어쓴다.
4. notifier와 web-backend forward를 **병렬**로 수행하고 둘 다 끝날 때까지 대기한다. 한쪽 실패가 다른 쪽을 막지 않는다.
5. **타임스탬프 위생:** incident의 `occurredAt`은 alert의 timestamp를 RFC 3339 또는 Unix epoch 정수 문자열로 파싱한 값. 파싱 불가이거나 2020-01-01 UTC 이전이면 서버 현재 시각으로 대체하고 경고 로그를 남긴다.
6. `test: true` alert도 동일하게 forward되며 incident에 `isTest`가 전파된다.
7. 부수 효과로 `POST /api/devices/seen`(alertState `"none"` 고정)을 fire-and-forget 호출한다.

### heartbeat 처리

- 필수 필드는 `deviceId`, `siteId`. 누락 시 무시 + 경고 로그.
- 토픽의 `{siteId}`로 페이로드 siteId를 덮어쓴다. `alertState` 누락 시 `"none"`으로 보정.
- 장비는 `siteId:deviceId` 복합 키로 식별한다 — 같은 deviceId라도 사이트가 다르면 별개 장비다.
- 처음 보는 키는 즉시 등록되고, 수신 시 `alive=true`, `lastHeartbeat=서버 수신 시각`, `alertState=수신값`으로 갱신된다.
- 부수 효과로 `POST /api/devices/seen`(수신한 alertState 전달)을 fire-and-forget 호출한다.

### dead 판정

- 5초 주기 검사로, `alive=true`인 장비가 `HEARTBEAT_TIMEOUT_SEC`를 초과해 heartbeat가 없으면 `alive=false`로 마킹한다. dead 마킹만으로는 목록에서 제거하지 않는다 — 제거는 아래 "장비 스토어 보존 상한·eviction" 계약의 eviction으로만 발생한다.
- 판정은 서버 수신 시각 기준이다(장비 timestamp 무관).
- 상태는 휘발성 — 프로세스 재시작 시 장비 목록은 비어 있고, 각 장비의 첫 heartbeat까지 그 장비는 목록에 없다.

### 장비 스토어 보존 상한 · eviction

**두 메커니즘의 역할은 분리된다 — 무한 증가 금지는 상한(LRU)이 확정 보장하고, TTL은 가시성용 선택적 청소일 뿐이다.**

- **불변식(무한 증가 금지) — 상한 eviction이 보장:** in-memory 장비 스토어의 항목 수는 무한히 증가하지 않으며 항상 `EQUIPMENT_MAX_DEVICES` 이하로 유지된다. 이 불변식은 **오직 상한(LRU) eviction으로만** 보장된다 — TTL 설정과 무관하게 성립한다. 새 키 등록으로 항목 수가 `EQUIPMENT_MAX_DEVICES`를 초과하려 하면 least-recently-seen(가장 오래 수신 없는) 장비부터 제거하여 상한을 유지한다. 오작동/악의적 publisher가 매 메시지마다 새로운 `siteId:deviceId` 키를 실어 보내더라도 메모리가 무제한으로 늘지 않는다.
- **TTL eviction — 선택적 청소(가시성):** 어떤 장비가 마지막으로 수신된 시각(heartbeat/alert/candidate 중 최신 서버 수신 시각)으로부터 `EQUIPMENT_EVICT_TTL_SEC`를 초과하면 스토어에서 **제거**되어 `GET /api/equipment/status` 응답에서 사라진다(dead로 남는 것이 아니라 항목 자체가 없어진다). 이는 메모리 상한을 지키기 위한 것이 아니라 **오래 방치된 dead 장비를 조회 화면에서 청소**하기 위한 선택적 기능이다.
- **기동 불변식(TTL vs dead 판정):** `EQUIPMENT_EVICT_TTL_SEC > HEARTBEAT_TIMEOUT_SEC`를 **기동 시 검증**한다. 위반(이하) 시 dead로 마킹되기도 전에 장비가 제거되어 dead 조회가 불가능해지므로, 기본값 `86400`으로 강제 대체하고 경고 로그를 남긴다(⚠ 대안: 기동 거부 — 배포 정책으로 확정). 이로써 dead로 마킹된 장비가 제거 전까지 일정 기간 조회 가능함이 보장된다.
- eviction된 장비가 이후 다시 heartbeat를 보내면 "처음 보는 키"와 동일하게 재등록된다(휘발성·무상태 원칙과 정합).
- **⚠ flood 시 정당 장비 축출 리스크(리뷰 항목):** 인증이 없는 브로커(interface-mqtt §브로커 접속 계약)에서 악의적/오작동 publisher가 매번 새 `siteId:deviceId`로 flood하면, 상한 eviction이 LRU 순으로 **정당한 실장비까지 축출**하여 조회 가시성을 파괴할 수 있다. 완화책(등록률 제한, 미검증 신규 키의 격리 버킷, web-backend 등록 완료 키 우선 보존 등)은 §⚠ 리뷰 필요 항목으로 남긴다 — 현 계약은 최소한 이 리스크의 존재를 명시한다.

### candidate 처리

- 필수 필드(`deviceId`, `siteId`, `class`, 양수 `confidence`, 양수 `threshold`) 검증 실패 시 무시 + 로그.
- 유효하면 로그 기록 + `POST /api/devices/seen`(alertState `"none"`)만 수행한다. **incident 생성 없음, notifier 호출 없음.**

### alert/resolved 양방향 동기화

- **웹 → 현장:** `POST /api/alert/resolved` 수신 시 `siteId` 필수(누락 시 400). 보정(`resolvedAt`, `resolvedBy.kind="web"`) 후 `safety/{siteId}/alert/resolved`로 발행. 브로커 연결 상태별 응답(미연결 503 / 단절 시 타임아웃 503 / 발행실패 500)은 아래 "MQTT 발행 API 공통 응답 계약"을 따른다.
- **현장 → 웹:** 구독으로 수신한 메시지의 siteId를 토픽 값으로 덮어쓴 뒤 `resolvedBy.kind`로 분기한다:
  - `"web"` → 자기 발행의 echo이므로 무시 (web-backend로 재전달하지 않는다 — 무한 루프 방지).
  - `"sensor_button"` → `POST /api/incidents/{incidentId}/resolve-from-sensor`로 forward. `incidentId == 0`이면 수신 측이 최근 미해결 incident에 매칭한다는 계약을 그대로 따른다.
  - 그 외 kind → 무시 + 로그.
- **재연결 중복 idempotency 계약:** gateway는 persistent session(clean=false)이므로 재연결 경계에서 브로커 세션 큐가 이 QoS1 `alert/resolved`를 **재전송**할 수 있다. `alertId` 기반 dedup은 `alert` 토픽에만 있고 resolved에는 없으므로, resolved forward의 중복 안전성은 **다운스트림 idempotency**에 의존한다: web-backend `resolve-from-sensor`는 이미 해소된 incident 재수신 시 **no-op**이어야 하며(중복 해소·중복 부수효과 금지), `incidentId==0` fallback도 이미 해소된 대상에는 재적용하지 않는다(`docs/spec/interface-mqtt.md` `alert/resolved` §핵심 로직 "재연결 중복과 idempotency"와 정합). web-kind echo는 무시되므로 재전송돼도 무해하다. 테스트 alert(`alertId` 부재)의 중복은 `alert` 토픽 QoS 2(exactly-once)가 막으므로 별도 forward-측 dedup이 불필요하다.

### restart 명령

- `siteId`와 `deviceId` 모두 필수 — 하나라도 없으면 400. 브로커 연결 상태별 응답(미연결 503 / 단절 시 타임아웃 503 / 발행실패 500)은 아래 "MQTT 발행 API 공통 응답 계약"을 따른다.
- 성공 시 `{"status":"sent","topic":"safety/{siteId}/cmd/restart"}` 반환.

### MQTT 발행 API 공통 응답 계약 (restart · alert/resolved · test-alert)

브로커 연결 상태에 따라 세 발행 엔드포인트의 응답이 달라진다.

> **변경 이력 (설계자 승인):** 이전 계약은 "연결 성립 후 브로커 단절" 시 발행 응답이 재연결까지 **무기한 블로킹(hang)**하는 것을 정상으로 규정했다. 무인 운영 환경에서 hang은 부적절하다고 판단되어, 발행 대기를 **유한 타임아웃(5초)**으로 제한하고 타임아웃 시 `503`을 반환하도록 변경했다. (구·리뷰 항목 8 해소)

- **최초 연결 미성립:** 프로세스 기동 후 브로커 연결이 **한 번도** 성립한 적 없는 동안은 `503` `{"error":"MQTT broker not connected"}`를 즉시 반환한다.
- **연결 성립 후 브로커 단절(자동 재연결 진행 중):** 연결 검사를 통과하여 발행이 클라이언트 내부 큐(in-memory)에 적재되나, 발행 완료 대기는 **서버 측 발행 타임아웃(5초)으로 제한된다**. 타임아웃 내에 전송이 완료되지 않으면 `503` `{"error":"MQTT publish timeout — broker unreachable"}`를 반환한다(무기한 hang 하지 않음).
- **발행 실패 판정 시:** `500` `{"error": ...}`.
- **성공:** `200` `{"status":"sent","topic":"<발행 토픽>"}`.

### test-alert

- `siteId` 누락 시 `"test"`, `deviceId` 누락 시 `"TEST-DEVICE"`로 기본값 적용. 발행되는 테스트 alert 페이로드의 스키마 상세는 `docs/spec/interface-mqtt.md`(alert 계약)가 소유한다.
- 실제 alert와 동일한 토픽으로 발행하므로, 자기 자신의 alert 구독으로 되돌아와 **실제 alert 파이프라인 전체**(notifier + incident 기록, `isTest` 표식 포함)를 통과한다. 이것이 의도된 end-to-end 테스트 경로다.

### 회복력

- 브로커 접속 실패 시 지수 백오프(1s→…→60s)로 무한 재시도한다. 그동안 조회성 HTTP 엔드포인트는 **hang 없이 즉시 응답**하되, `/api/equipment/status`는 `200`으로 현재 in-memory 상태를 반환하고, `/healthz`는 MQTT 연결·구독 상태를 반영하여 미연결/미구독 시 `503`(degraded)를 반환한다("헬스체크(healthz) 계약" 참조). 발행 엔드포인트의 브로커 단절 시 동작은 "MQTT 발행 API 공통 응답 계약" 참조.
- 재연결 성공 시 4개 토픽을 모두 자동 재구독한다. 단 **healthy 판정에 쓰이는 필수 구독은 경보성 3토픽(alert/heartbeat/resolved)** 이며, 이 중 하나라도 구독이 성립하지 않은 동안 `/healthz`가 degraded(503)로 표면화한다(candidate 구독 미성립은 판정 제외 — "헬스체크(healthz) 계약" 참조).
- 브로커 다운 중 하드웨어가 발행한 메시지에 대한 **로컬(gateway 측) 버퍼는 없다.** 단, gateway는 **persistent session(clean session=false) + 고정 client ID**로 접속하므로, gateway가 잠시 끊겼다 재접속하는 경계에서 브로커는 세션 큐에 보관한 QoS1/2 메시지를 재전송한다 — 이로써 재연결 경계의 **수신측 경보 유실을 방지**하는 것이 계약이다(설계자 승인). 이 보장은 **브로커(mosquitto)의 세션 지속성 설정에 의존**하며, 그 지속성 구성은 구현 단계에서 함께 갖춘다. 브로커 자체가 죽어 있던 구간에 하드웨어가 발행한 메시지는 QoS에 따른 하드웨어(발행측) 재전송에 의존하며, 그 밖의 유실은 허용한다.

## 검증 단언 (TDD)

각 단언은 컨테이너 네트워크 내부에서 실행한다 (`GW=http://sentinel-hw-gateway:8080`, mosquitto 컨테이너에서 `mosquitto_pub/sub`).

- **A. 헬스체크 (정상):** 브로커 연결·경보성 필수 3토픽(alert/heartbeat/resolved) 구독(SUBACK granted)이 성립한 상태에서 `curl -s -o /dev/null -w '%{http_code}' $GW/healthz` → `200`, 본문에 `"status":"ok"` 포함. (candidate 구독은 healthy 판정에 무관.)

- **A2. 헬스체크 degraded (MQTT 미연결/미구독):** 브로커에 연결되지 않은 상태(최초 연결 전, 또는 단절을 클라이언트가 인지한 후)에서 `curl --max-time 2 -s -w '%{http_code}' $GW/healthz` → **`503`**, 본문에 `"status":"degraded"` 포함하며 응답은 hang 없이 **1초 이내**에 반환된다(발행 타임아웃 5초와 구분되는 in-memory 플래그 조회). 이후 mosquitto 기동으로 연결·경보성 3토픽 재구독이 성립하면 별도 조치 없이 A(200/`"status":"ok"`)가 다시 성립한다.

- **B. heartbeat → 장비 등록:** 새 deviceId로 `mosquitto_pub -t safety/site1/heartbeat -m '{"deviceId":"T-B1","siteId":"site1","status":"running","alertState":"none","timestamp":"<now>"}'` 발행 후 `GET /api/equipment/status` 응답에 `{"deviceId":"T-B1","siteId":"site1","alive":true,"alertState":"none",...}` 항목이 존재한다. `lastHeartbeat`는 발행 시각 ±5초 이내의 서버 시각(RFC 3339)이다.

- **C. dead 판정 (TTL 제거와 격리):** 기본/일반 설정 인스턴스(`EQUIPMENT_EVICT_TTL_SEC ≫ HEARTBEAT_TIMEOUT_SEC`, 예: 86400 ≫ 30)에서 B 이후 heartbeat를 중단하고 `HEARTBEAT_TIMEOUT_SEC + 10`초 대기 → 같은 장비의 `alive`가 `false`이고 항목은 목록에 **남아 있다**(TTL 미도달이므로 제거되지 않음). *R의 소TTL 설정과 같은 인스턴스에서 돌리지 않는다 — 두 단언은 서로 다른 config 인스턴스로 분리 실행한다.*

- **D. alertState 전파:** `alertState:"active"`인 heartbeat 발행 → `GET /api/equipment/status`에서 해당 장비 `alertState == "active"`.

- **E. alert 이중 forward:** 필수 필드를 갖춘 alert를 `safety/site1/alert`(QoS 2)로 발행 → notifier가 `POST /api/notify`(원본 alert 페이로드)를, web-backend가 `POST /api/incidents`(`{siteId, deviceId, alertId, description, occurredAt, isTest}`)를 각각 1회 수신한다. `occurredAt == alert.timestamp`(유효 시각일 때).

- **F. alertId dedup:** 동일 `alertId`의 alert를 2회 발행(web-backend가 각 forward에 2xx 응답) → notifier/web-backend forward는 정확히 1회씩만 발생한다. (첫 forward가 2xx로 성공해 `alertId`가 캐시에 등록되어야 2회차가 dedup된다.)

- **G. 필수 필드 누락 alert 무시:** `type` 누락 alert 발행 → notifier/web-backend에 어떤 호출도 발생하지 않고, 로그에 "Missing required fields"가 남는다.

- **H. 타임스탬프 위생:** `timestamp:"1970-01-01T00:00:00Z"`인 alert 발행 → incident의 `occurredAt`이 발행 시각 ±10초 이내의 서버 시각으로 대체된다.

- **I. siteId 토픽 우선:** 페이로드 `siteId:"siteY"`인 alert를 `safety/siteX/alert`로 발행 → forward된 페이로드의 siteId는 `siteX`다.

- **J. restart 발행:** `mosquitto_sub -t 'safety/site1/cmd/restart'` 대기 중 `curl -X POST $GW/api/restart -d '{"siteId":"site1","deviceId":"T-J1","requestedBy":"tester","reason":"spec"}'` → HTTP 200 + `{"status":"sent","topic":"safety/site1/cmd/restart"}`, 구독자는 `deviceId/siteId/requestedBy/reason` 이 요청과 일치하고 `timestamp`가 채워진 메시지를 수신한다. `siteId` 누락 요청은 `400`.

- **K. 웹 해소 발행 + echo 무시:** `mosquitto_sub -t 'safety/site1/alert/resolved'` 대기 중 `curl -X POST $GW/api/alert/resolved -d '{"incidentId":1,"siteId":"site1","resolvedBy":{"kind":"web","id":"admin","label":"관리자"}}'` → HTTP 200, 구독자가 메시지 1건 수신, 그리고 web-backend의 `resolve-from-sensor` 엔드포인트는 **호출되지 않는다** (echo 무시).

- **L. 센서 해소 forward:** `mosquitto_pub -t safety/site1/alert/resolved -m '{"incidentId":0,"siteId":"site1","resolvedAt":"<now>","resolvedBy":{"kind":"sensor_button","id":"T-L1","label":"reset"}}'` → web-backend가 `POST /api/incidents/0/resolve-from-sensor`를 동일 페이로드로 1회 수신한다. `kind:"unknown"`으로 바꿔 발행하면 어떤 forward도 없다.

- **M. candidate는 참고용:** 유효한 candidate 발행 → `POST /api/devices/seen`(alertState `"none"`)만 호출되고 `POST /api/incidents`·`POST /api/notify`는 호출되지 않는다.

- **N. test-alert 순환:** `curl -X POST $GW/api/test-alert -d '{}'` → HTTP 200, `safety/test/alert`에 `test:true, deviceId:"TEST-DEVICE"` 메시지가 발행되고, 그 결과 web-backend `POST /api/incidents`에 `isTest:true`인 incident가 도달한다.

- **O. 브로커 다운 내성 (최초 연결 전):** mosquitto 정지 상태에서 hw-gateway를 (재)기동한 직후 — 즉 최초 브로커 연결이 아직 성립하지 않은 동안 — `GET /healthz`는 **`503`(`"status":"degraded"`)**, `POST /api/restart`는 `503`을 반환한다(둘 다 hang 없이 즉시). HTTP 서버 자체는 기동·응답한다. 이후 mosquitto 기동 시 별도 조치 없이 A(200/ok)와 B 단언이 성립한다(자동 연결·구독).

- **O2. 브로커 단절 중 발행 타임아웃 503 (연결 성립 후):** 브로커 연결이 한 번 성립한 뒤 mosquitto를 정지한 상태에서 `curl --max-time 10 -X POST $GW/api/restart -d '{"siteId":"site1","deviceId":"T-O1"}'`는 **유한 시간(발행 타임아웃 5초 + 여유) 내에 `503`** `{"error":"MQTT publish timeout — broker unreachable"}`를 반환한다(무기한 hang 하지 않음 — curl exit 28 아님). 발행 타임아웃은 클라이언트의 단절 인지와 무관하게 5초로 유한하다.
  - 같은 상태에서 `GET /healthz`는 각 호출이 **hang 없이 1초 이내에 응답**한다(발행 타임아웃 5초와 구분되는 in-memory 플래그 조회). 다만 `"status":"degraded"`(503)로의 **전이 시점**은 "즉시"가 아니라 클라이언트가 단절을 인지하는 **keep-alive/PING 감지 경계 이내**다(조용한 브로커 다운은 keep-alive 만료로 감지). 따라서 단언 판정은 "브로커 정지 후 (설정 keep-alive/PING 경계 + 여유) 시점에 healthz가 503/degraded"로 한다. 짧은 keep-alive/PING(≤5s 권장) 구성 시 이 경계는 발행 타임아웃과 정합한다. (설계자 승인 변경: 이전 계약의 무기한 발행 블로킹을 유한 타임아웃 503으로 대체 · healthz "즉시 503"을 감지 경계 이내로 정정)

- **P. 휘발성:** hw-gateway 재시작 직후 `GET /api/equipment/status`는 빈 배열 `[]`이다.

- **Q. 장비 스토어 상한 (무한 증가 금지):** `EQUIPMENT_MAX_DEVICES`를 작은 값(예: 5)으로 설정한 인스턴스에 서로 다른 deviceId로 상한을 초과하는 수(예: 20건)의 heartbeat를 발행 → `GET /api/equipment/status` 배열 길이는 항상 `EQUIPMENT_MAX_DEVICES`(=5) 이하이며, 가장 최근에 수신된 장비들이 남는다(least-recently-seen 순으로 제거).

- **R. TTL eviction (C와 격리, 기동 불변식 준수):** **기동 불변식 `EQUIPMENT_EVICT_TTL_SEC > HEARTBEAT_TIMEOUT_SEC`를 만족하도록** 두 값을 모두 작게 설정한 전용 인스턴스(예: `HEARTBEAT_TIMEOUT_SEC=3`, `EQUIPMENT_EVICT_TTL_SEC=10`)에서 장비 하나를 heartbeat로 등록한 뒤 TTL + eviction 주기 여유를 두고 재수신을 중단 → `GET /api/equipment/status`에서 해당 장비 항목이 **사라진다**(dead=false로 남는 것이 아니라 항목 제거). 같은 조건에서 TTL 이내에 재수신하면 항목은 유지된다. *C(dead 유지)와는 별개 인스턴스로 실행하여 상호오염을 제거한다.*

- **T. 기동 불변식 (TTL > HEARTBEAT):** `EQUIPMENT_EVICT_TTL_SEC`를 `HEARTBEAT_TIMEOUT_SEC` 이하(예: `HEARTBEAT_TIMEOUT_SEC=30`, `EQUIPMENT_EVICT_TTL_SEC=10`)로 설정해 기동 → gateway는 잘못된 TTL을 채택하지 않는다: 기동 로그에 경고를 남기고 `EQUIPMENT_EVICT_TTL_SEC`를 기본값 `86400`으로 강제 대체한다(그 결과 이 인스턴스에서 R의 소TTL 제거는 관측되지 않는다). ⚠ 대안(기동 거부)을 배포 정책으로 택한 경우, 프로세스가 비정상 종료 코드로 기동 실패한다.

- **S. 재연결 경계 수신측 유실 방지 (gateway만 재기동, 브로커 상시가동):** **전제 — mosquitto는 이 시나리오 내내 상시 가동한다(브로커를 재기동하지 않는다).** gateway가 QoS1 이상(alert는 QoS2)으로 구독·persistent session(clean=false)+고정 clientID로 접속한 상태에서 **gateway만** 짧게 정지→재기동하고, 그 정지 구간 동안(브로커는 계속 살아 있음) 하드웨어가 `safety/site1/alert`로 alert 1건을 발행한다. gateway 재접속 후 브로커 세션 큐가 미전달 메시지를 재전송하여 해당 alert가 `POST /api/incidents`로 forward되어 유실되지 않는다.
  - 이 조건(브로커 상시가동)에서는 세션 큐가 **브로커 메모리에 유지**되므로 mosquitto의 디스크 persistence 없이도 성립한다.
  - **브로커 자체가 재기동되는 경우**의 유실 방지는 별도 계약 산출물인 mosquitto 디스크 persistence(`persistence true` + persistence 볼륨 마운트, `docs/spec/interface-mqtt.md` §브로커 접속 계약 "세션 지속성")에 의존하며, 그 설정이 없으면 브로커 재기동 구간의 재전송은 보장되지 않는다(현 스택에서 미구성 시 이 확장 시나리오는 실측 불가).

## ⚠️ 리뷰 필요 (의도 불확실)

1. **notifier 재시도 부재 vs 문서 불일치.** 구현은 notifier forward를 재시도 없이 1회만 시도하고, web-backend forward는 transport 에러 및 HTTP 5xx에 대해 3회 재시도한다(4xx는 미재시도). 그러나 레거시 서비스 가이드(`docs/services/hw-gateway.md` "Outbound Calls")는 notifier에 "exponential backoff + jitter 재시도"가 있다고 기술한다 — notifier 재시도 여부가 어느 것이 의도인지 확정 필요. 본 스펙은 코드 동작(notifier 0회, web-backend 3회)을 계약으로 적었다.

2. **`POST /api/restart` 응답 코드.** 서비스 문서는 `202 Accepted`라 하나 구현은 `200 OK`를 반환한다. 또한 문서의 요청 예시는 `{deviceId}`뿐이지만 구현은 `siteId`도 필수(누락 시 400)다. 본 스펙은 코드 기준(200, siteId 필수)으로 적었다.

3. **`POST /api/test-alert`가 레거시 서비스 문서 HTTP API 표에 없음.** 현재는 `docs/spec/interface-web-api.md` 계약 15에 HTTP 계약이 있고, `docs/spec/interface-mqtt.md` 본문(alert 계약)도 테스트 발행 방향·스키마·`alertId` 부재를 계약으로 기술한다. 그러나 레거시 서비스 문서(HTTP API 표, Code Structure의 엔드포인트 목록)에는 여전히 없다. 공개 계약인지, 비공개 내부용인지 확정 필요.

4. **~~dedup 등록 시점이 forward 성공 이전.~~ (해소됨)** `alertId` 캐시 등록을 web-backend forward가 2xx로 성공한 이후로 이동했다. forward가 최종 실패(전송 오류/5xx 재시도 소진)하면 `alertId`가 미등록으로 남아, 동일 `alertId`의 MQTT 재전송이 다시 forward되어 유실을 복구할 수 있다 — `docs/spec/interface-mqtt.md` 재전송 정책 의도와 정합. (핵심 로직 §alert 처리 2 반영)

5. **alert 핸들러가 forward 완료까지 블로킹.** alert 처리는 두 forward가 끝날 때까지 MQTT 콜백 안에서 대기하며, web-backend 재시도 포함 시 수십 초까지 걸릴 수 있다. MQTT 클라이언트의 순서 보장 모드에서 후속 메시지(heartbeat 등) 처리가 그동안 지연될 수 있다 — 의도된 배압인지 확인 필요.

6. **heartbeat의 `alertId` 필드 미사용.** `docs/spec/interface-mqtt.md`의 heartbeat 계약은 optional `alertId`(발동 중 alert 식별)를 정의하고 구현도 파싱하지만, 어디에도 사용·전파하지 않는다. 향후 사용 예정인지, 계약에서 제거할지 확정 필요.

7. **`event/candidate` 구독이 레거시 서비스 문서에 없음.** `docs/spec/interface-mqtt.md`와 코드는 4개 토픽 구독이지만 레거시 서비스 문서 "Code Structure"는 3개 토픽만 기술한다(문서 누락으로 추정).

8. **~~브로커 단절 중 발행 요청의 무기한 블로킹이 의도인지 불확실.~~ (해소됨 — 설계자 승인)** 연결이 한 번 성립한 뒤 브로커가 죽는 자연스러운 운영 시나리오(가동 중 브로커 다운)에서 무기한 hang이 부적절하다고 판단되어, 발행 3종 API의 대기를 유한 타임아웃(5초)으로 제한하고 타임아웃 시 `503`을 반환하도록 변경했다. 발행 핸들러의 `token.Wait()`를 `token.WaitTimeout(5s)`로 교체. (핵심 로직 §MQTT 발행 API 공통 응답 계약, 단언 O2 반영)

9. **[해소 — 설계자 승인] persistent session 채택.** 재연결 경계의 **수신측** 경보 유실을 막기 위해 gateway를 persistent session(clean session=false) + 고정 clientID로 접속하는 것을 계약으로 확정했다(§의존 도구 표, §회복력, 단언 S). 이 유실 방지 보장은 브로커(mosquitto)가 세션·큐를 실제로 지속(`persistence`/QoS 큐 유지, gateway 오프라인 동안 in-flight 메시지 보관)하도록 설정되어야 성립하므로, 구현 단계에서 mosquitto 세션 지속성 구성을 함께 갖춘다.

10. **healthz 의미 변경(경보 유실 은폐 제거) — 계약 강화.** `/healthz`를 "HTTP 서버 생존"에서 "MQTT 연결 + 경보성 필수 3토픽(alert/heartbeat/resolved) 구독 성립"으로 강화하여(candidate는 유실 허용 참고 채널이라 판정 제외), 브로커 미연결/미구독을 degraded(503)로 표면화하도록 계약을 바꿨다(§헬스체크(healthz) 계약, 단언 A/A2/O/O2). compose healthcheck가 이 엔드포인트를 쓰므로, 컨테이너 healthcheck의 재시작/재기동 정책과의 상호작용(브로커 재기동 지연 동안 gateway 컨테이너가 unhealthy로 판정되어 불필요 재시작되지 않는지)은 배포 시 확인 권장.

11. **장비 스토어 eviction 도입 — /api/equipment/status 노출 계약 변경.** deviceId 무제한 증가에 의한 메모리 고갈을 막기 위해 보존 상한(`EQUIPMENT_MAX_DEVICES`) + TTL(`EQUIPMENT_EVICT_TTL_SEC`) 기반 eviction을 계약으로 도입했다(§장비 스토어 보존 상한·eviction, 단언 Q/R). 결과로 오래(비활성) 방치된 dead 장비는 `GET /api/equipment/status`에서 사라진다. 기본 상한/TTL 값(1000 / 86400s)은 현장 장비 대수·조회 요구를 반영해 조정 가능한 설정값이다.

12. **⚠ 상한 eviction의 flood 취약성 — 정당 장비 축출.** 인증 없는 브로커에서 악의적/오작동 publisher가 매번 새 `siteId:deviceId`로 flood하면 LRU 상한 eviction이 정당한 실장비까지 축출해 조회 가시성을 파괴할 수 있다(§장비 스토어 보존 상한·eviction의 리스크 노트). 완화책 확정 필요: (a) 신규 키 **등록률 제한**, (b) 미검증 신규 키를 **격리 버킷**에 두고 검증 후 승격, (c) web-backend 등록 완료 키 **우선 보존**. 현 계약은 리스크 명시까지만 반영했고 완화 메커니즘 채택은 미결.

13. **⚠ healthz 단절 감지 지연 vs keep-alive 값.** healthz의 degraded 전이는 클라이언트의 단절 인지 시점(keep-alive/PING 경계) 기준이므로, "발행 503(5초)"과 시점을 맞추려면 gateway keep-alive/PING을 ≤5s로 짧게 두어야 한다(§헬스체크 계약, 단언 O2). interface-mqtt의 60s 권장은 H/W 디바이스 대상이며 gateway는 별도 튜닝 가능. 짧은 keep-alive의 브로커 부하 트레이드오프를 고려해 최종 값 확정 필요.

14. **⚠ TTL 불변식 위반 시 정책 — 기본값 대체 vs 기동 거부.** `EQUIPMENT_EVICT_TTL_SEC ≤ HEARTBEAT_TIMEOUT_SEC` 위반을 기동 검증 불변식으로 승격했다(§장비 스토어 보존 상한·eviction, env 표, 단언 T). 현 계약은 기존 "잘못된 값→기본값" 패턴과 정합하도록 **기본값 강제 대체 + 경고**를 채택했으나, 오설정을 조용히 흡수하지 않는 **기동 거부(fail-fast)**가 더 안전할 수 있다 — 배포 정책으로 확정 필요.
