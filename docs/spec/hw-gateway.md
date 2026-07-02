# hw-gateway 스펙

> 상태: 살아있는 계약 (living spec) · 독자: 스펙 작성자

## 목적 / 의도

hw-gateway는 현장 하드웨어(MQTT)와 소프트웨어 서비스(HTTP) 사이의 **유일한 변환 접점**이다.

- 하드웨어가 MQTT로 보내는 위급 알림(alert)·생존 신호(heartbeat)·후보 탐지(candidate)·해소 통지(resolved)를 수신하여, 소프트웨어 측(notifier, web-backend)에 HTTP로 전달한다.
- 소프트웨어 측에서 온 명령(재시작, 웹 해소, 테스트 알림)을 MQTT 발행으로 변환하여 하드웨어에 전달한다.
- 장비별 생존(alive/dead) 상태와 경보 상태를 in-memory로 유지하고 조회 API로 노출한다.

의도적으로 **상태 비저장(휘발성)** 이다. 영속 저장은 하지 않으며, 장비의 영속 등록은 web-backend에 위임한다(best-effort 통지).

## 언어 · 런타임

- Go (단일 바이너리, 단일 프로세스).
- Docker 컨테이너로 실행되며 내부 포트 8080에서 HTTP를 서비스한다. 외부 포트 노출 없음.

## 의존 도구 · 시스템

| 의존 대상 | 방식 | 필수 여부 |
|-----------|------|-----------|
| MQTT 브로커 (mosquitto) | subscribe + publish, client ID `sentinel-hw-gateway`, clean session, keep-alive 60s, 자동 재연결(지수 백오프 최대 60s) | 필수 — 단 브로커가 죽어도 HTTP 서버는 기동·응답 유지 |
| notifier | outbound HTTP (`POST /api/notify`) | alert 전달 대상 |
| web-backend | outbound HTTP (`POST /api/incidents`, `POST /api/devices/seen`, `POST /api/incidents/{id}/resolve-from-sensor`) | alert 기록·장비 등록·센서 해소 전달 대상 |

환경 변수 계약:

| 변수 | 기본값 | 의미 |
|------|--------|------|
| `MQTT_BROKER_URL` | `tcp://mosquitto:1883` | 브로커 주소 |
| `NOTIFIER_URL` | `http://notifier:8080` | notifier 베이스 URL |
| `WEB_BACKEND_URL` | `http://web-backend:8080` | web-backend 베이스 URL |
| `HEARTBEAT_TIMEOUT_SEC` | `30` | 이 시간 초과 미수신 시 장비 dead 판정 (양의 정수만 유효, 그 외는 기본값) |

## 입력

### MQTT 구독 (토픽·페이로드 스키마의 소유자는 `docs/interfaces/mqtt-publisher-guide.md` — 여기서 재정의하지 않음)

| 토픽 | QoS | 의미 |
|------|-----|------|
| `safety/+/alert` | 2 | 위급 알림 |
| `safety/+/heartbeat` | 0 | 장비 생존 신호 |
| `safety/+/alert/resolved` | 1 | 위급 해소 통지 (양방향 — 자기 발행 echo 포함) |
| `safety/+/event/candidate` | 0 | threshold 미달 위기 키워드 탐지 (참고용) |

### Inbound HTTP (요청/응답 스키마의 소유자는 `docs/services/hw-gateway.md` "HTTP API")

| 엔드포인트 | 의미 |
|-----------|------|
| `GET /healthz` | 헬스체크 |
| `GET /api/equipment/status` | in-memory 장비 상태 조회 |
| `POST /api/restart` | 재시작 명령 → MQTT 발행 |
| `POST /api/test-alert` | 테스트 alert → MQTT 발행 (실제 alert 경로로 순환 유입됨) |
| `POST /api/alert/resolved` | 웹 발 해소 통지 → MQTT 발행 |

## 출력 (계약)

### MQTT 발행 (스키마 소유자: `docs/interfaces/mqtt-publisher-guide.md`)

- `safety/{siteId}/cmd/restart` — QoS 1, retain false. 발행 시각(timestamp)은 서버가 UTC로 채운다.
- `safety/{siteId}/alert/resolved` — QoS 1, retain false. `resolvedAt` 누락 시 서버 UTC 현재 시각, `resolvedBy.kind` 누락 시 `"web"`으로 보정 후 발행.
- `safety/{siteId}/alert` — QoS 2, retain false. 테스트 알림 전용(`test: true` 고정, type `test`, severity `critical`).

### Outbound HTTP (페이로드 형태의 소유자: `docs/services/hw-gateway.md` "Outbound Calls")

| 호출 | 트리거 | 신뢰성 계약 |
|------|--------|-------------|
| notifier `POST /api/notify` | alert 수신 | 타임아웃 10초, **재시도 없음**, 실패는 로그만 |
| web-backend `POST /api/incidents` | alert 수신 (notifier 호출과 병렬) | 시도당 타임아웃 10초, 실패 시 최대 3회 재시도 (지수 백오프 1s 기점 ×2, ±25% jitter) |
| web-backend `POST /api/devices/seen` | heartbeat / alert / candidate 수신 시마다 | best-effort — 타임아웃 5초, 재시도 없음, 실패해도 본 처리 계속 |
| web-backend `POST /api/incidents/{id}/resolve-from-sensor` | `alert/resolved` 수신 + 해소 주체가 sensor_button | 타임아웃 5초, 재시도 없음. 수신 페이로드를 그대로 전달 |

### 장비 상태 조회 응답

`GET /api/equipment/status`는 알려진 모든 장비의 배열 `[{deviceId, siteId, alive, lastHeartbeat, alertState}]`을 반환한다. `lastHeartbeat`는 **서버 수신 시각**(UTC, RFC 3339)이며 장비가 보낸 timestamp가 아니다. `alertState`는 `"none" | "active"`.

## 핵심 로직 (동작)

### alert 처리

1. JSON 파싱 실패 또는 필수 필드(`deviceId`, `siteId`, `type`, `timestamp`) 누락 시 메시지를 무시하고 경고 로그만 남긴다 — 어떤 forward도 발생하지 않는다.
2. **중복 제거:** `alertId`가 있으면 in-memory 캐시로 dedup — 동일 `alertId` 재수신 시 forward 없이 무시. 캐시 항목은 24시간 후 청소(1시간 주기), 프로세스 재시작 시 초기화. `alertId` 누락 시 dedup 없이 처리 계속.
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

- 5초 주기 검사로, `alive=true`인 장비가 `HEARTBEAT_TIMEOUT_SEC`를 초과해 heartbeat가 없으면 `alive=false`로 마킹한다. 목록에서 제거하지 않는다.
- 판정은 서버 수신 시각 기준이다(장비 timestamp 무관).
- 상태는 휘발성 — 프로세스 재시작 시 장비 목록은 비어 있고, 각 장비의 첫 heartbeat까지 그 장비는 목록에 없다.

### candidate 처리

- 필수 필드(`deviceId`, `siteId`, `class`, 양수 `confidence`, 양수 `threshold`) 검증 실패 시 무시 + 로그.
- 유효하면 로그 기록 + `POST /api/devices/seen`(alertState `"none"`)만 수행한다. **incident 생성 없음, notifier 호출 없음.**

### alert/resolved 양방향 동기화

- **웹 → 현장:** `POST /api/alert/resolved` 수신 시 `siteId` 필수(누락 시 400). 보정(`resolvedAt`, `resolvedBy.kind="web"`) 후 `safety/{siteId}/alert/resolved`로 발행. 브로커 미연결 시 503, 발행 실패 시 500.
- **현장 → 웹:** 구독으로 수신한 메시지의 siteId를 토픽 값으로 덮어쓴 뒤 `resolvedBy.kind`로 분기한다:
  - `"web"` → 자기 발행의 echo이므로 무시 (web-backend로 재전달하지 않는다 — 무한 루프 방지).
  - `"sensor_button"` → `POST /api/incidents/{incidentId}/resolve-from-sensor`로 forward. `incidentId == 0`이면 수신 측이 최근 미해결 incident에 매칭한다는 계약을 그대로 따른다.
  - 그 외 kind → 무시 + 로그.

### restart 명령

- `siteId`와 `deviceId` 모두 필수 — 하나라도 없으면 400. 브로커 미연결 시 503. 발행 실패 시 500.
- 성공 시 `{"status":"sent","topic":"safety/{siteId}/cmd/restart"}` 반환.

### test-alert

- `siteId` 누락 시 `"test"`, `deviceId` 누락 시 `"TEST-DEVICE"`로 기본값 적용.
- 실제 alert와 동일한 토픽·QoS 2로 발행하므로, 자기 자신의 alert 구독으로 되돌아와 **실제 alert 파이프라인 전체**(notifier + incident 기록, `isTest` 표식 포함)를 통과한다. 이것이 의도된 end-to-end 테스트 경로다.

### 회복력

- 브로커 접속 실패 시 지수 백오프(1s→…→60s)로 무한 재시도하며, 그동안 HTTP 서버는 정상 응답한다.
- 재연결 성공 시 4개 토픽을 자동 재구독한다.
- 브로커 다운 중 하드웨어가 발행한 메시지에 대한 로컬 버퍼는 없다 — QoS에 따른 브로커/클라이언트 재전송 외 유실을 허용한다.

## 검증 단언 (TDD)

각 단언은 컨테이너 네트워크 내부에서 실행한다 (`GW=http://sentinel-hw-gateway:8080`, mosquitto 컨테이너에서 `mosquitto_pub/sub`).

- **A. 헬스체크:** `curl -s -o /dev/null -w '%{http_code}' $GW/healthz` → `200`, 본문에 `"status":"ok"` 포함.

- **B. heartbeat → 장비 등록:** 새 deviceId로 `mosquitto_pub -t safety/site1/heartbeat -m '{"deviceId":"T-B1","siteId":"site1","status":"running","alertState":"none","timestamp":"<now>"}'` 발행 후 `GET /api/equipment/status` 응답에 `{"deviceId":"T-B1","siteId":"site1","alive":true,"alertState":"none",...}` 항목이 존재한다. `lastHeartbeat`는 발행 시각 ±5초 이내의 서버 시각(RFC 3339)이다.

- **C. dead 판정:** B 이후 heartbeat를 중단하고 `HEARTBEAT_TIMEOUT_SEC + 10`초 대기 → 같은 장비의 `alive`가 `false`이고 항목은 목록에 남아 있다.

- **D. alertState 전파:** `alertState:"active"`인 heartbeat 발행 → `GET /api/equipment/status`에서 해당 장비 `alertState == "active"`.

- **E. alert 이중 forward:** 필수 필드를 갖춘 alert를 `safety/site1/alert`(QoS 2)로 발행 → notifier가 `POST /api/notify`(원본 alert 페이로드)를, web-backend가 `POST /api/incidents`(`{siteId, deviceId, description, occurredAt, isTest}`)를 각각 1회 수신한다. `occurredAt == alert.timestamp`(유효 시각일 때).

- **F. alertId dedup:** 동일 `alertId`의 alert를 2회 발행 → notifier/web-backend forward는 정확히 1회씩만 발생한다.

- **G. 필수 필드 누락 alert 무시:** `type` 누락 alert 발행 → notifier/web-backend에 어떤 호출도 발생하지 않고, 로그에 "Missing required fields"가 남는다.

- **H. 타임스탬프 위생:** `timestamp:"1970-01-01T00:00:00Z"`인 alert 발행 → incident의 `occurredAt`이 발행 시각 ±10초 이내의 서버 시각으로 대체된다.

- **I. siteId 토픽 우선:** 페이로드 `siteId:"siteY"`인 alert를 `safety/siteX/alert`로 발행 → forward된 페이로드의 siteId는 `siteX`다.

- **J. restart 발행:** `mosquitto_sub -t 'safety/site1/cmd/restart'` 대기 중 `curl -X POST $GW/api/restart -d '{"siteId":"site1","deviceId":"T-J1","requestedBy":"tester","reason":"spec"}'` → HTTP 200 + `{"status":"sent","topic":"safety/site1/cmd/restart"}`, 구독자는 `deviceId/siteId/requestedBy/reason` 이 요청과 일치하고 `timestamp`가 채워진 메시지를 수신한다. `siteId` 누락 요청은 `400`.

- **K. 웹 해소 발행 + echo 무시:** `mosquitto_sub -t 'safety/site1/alert/resolved'` 대기 중 `curl -X POST $GW/api/alert/resolved -d '{"incidentId":1,"siteId":"site1","resolvedBy":{"kind":"web","id":"admin","label":"관리자"}}'` → HTTP 200, 구독자가 메시지 1건 수신, 그리고 web-backend의 `resolve-from-sensor` 엔드포인트는 **호출되지 않는다** (echo 무시).

- **L. 센서 해소 forward:** `mosquitto_pub -t safety/site1/alert/resolved -m '{"incidentId":0,"siteId":"site1","resolvedAt":"<now>","resolvedBy":{"kind":"sensor_button","id":"T-L1","label":"reset"}}'` → web-backend가 `POST /api/incidents/0/resolve-from-sensor`를 동일 페이로드로 1회 수신한다. `kind:"unknown"`으로 바꿔 발행하면 어떤 forward도 없다.

- **M. candidate는 참고용:** 유효한 candidate 발행 → `POST /api/devices/seen`(alertState `"none"`)만 호출되고 `POST /api/incidents`·`POST /api/notify`는 호출되지 않는다.

- **N. test-alert 순환:** `curl -X POST $GW/api/test-alert -d '{}'` → HTTP 200, `safety/test/alert`에 `test:true, deviceId:"TEST-DEVICE"` 메시지가 발행되고, 그 결과 web-backend `POST /api/incidents`에 `isTest:true`인 incident가 도달한다.

- **O. 브로커 다운 내성:** mosquitto 정지 상태에서 `GET /healthz`는 `200`, `POST /api/restart`는 `503`을 반환한다. mosquitto 재기동 후 별도 조치 없이 B 단언이 다시 성립한다(자동 재연결·재구독).

- **P. 휘발성:** hw-gateway 재시작 직후 `GET /api/equipment/status`는 빈 배열 `[]`이다.

## ⚠️ 리뷰 필요 (의도 불확실)

1. **notifier 재시도 부재 vs 문서 불일치.** 구현은 notifier forward를 재시도 없이 1회만 시도하고, web-backend forward는 3회 재시도한다. 그러나 `docs/services/hw-gateway.md` "Outbound Calls"는 notifier에 "exponential backoff + jitter 재시도"가 있다고 기술하고, `docs/interfaces/mqtt-publisher-guide.md` §8은 web-backend 실패 시 "1초 후 1회 재시도"라고 기술한다. 세 곳(코드/서비스 문서/인터페이스 문서)이 모두 다르다 — 어느 것이 의도인지 확정 필요. 본 스펙은 코드 동작(notifier 0회, web-backend 3회)을 계약으로 적었다.

2. **`POST /api/restart` 응답 코드.** 서비스 문서는 `202 Accepted`라 하나 구현은 `200 OK`를 반환한다. 또한 문서의 요청 예시는 `{deviceId}`뿐이지만 구현은 `siteId`도 필수(누락 시 400)다. 본 스펙은 코드 기준(200, siteId 필수)으로 적었다.

3. **`POST /api/test-alert`가 서비스 문서 HTTP API 표에 없음.** 구현·주석상 의도된 엔드포인트로 보이나 서비스 문서(HTTP API 표, Code Structure의 엔드포인트 목록)와 인터페이스 문서 어디에도 계약이 없다. 문서 누락인지, 비공개 내부용인지 확정 필요.

4. **dedup 등록 시점이 forward 성공 이전.** `alertId`는 forward 시도 전에 처리됨으로 기록된다. 따라서 notifier/web-backend forward가 전부 실패해도 동일 `alertId`의 MQTT 재전송(펌웨어 재전송 정책의 핵심 시나리오)은 24시간 동안 다시 forward되지 않는다. "재전송으로 유실 복구"라는 인터페이스 문서의 의도와 충돌할 수 있다.

5. **alert 핸들러가 forward 완료까지 블로킹.** alert 처리는 두 forward가 끝날 때까지 MQTT 콜백 안에서 대기하며, web-backend 재시도 포함 시 수십 초까지 걸릴 수 있다. MQTT 클라이언트의 순서 보장 모드에서 후속 메시지(heartbeat 등) 처리가 그동안 지연될 수 있다 — 의도된 배압인지 확인 필요.

6. **heartbeat의 `alertId` 필드 미사용.** 인터페이스 문서 §4는 heartbeat에 optional `alertId`(발동 중 alert 식별)를 정의하고 구현도 파싱하지만, 어디에도 사용·전파하지 않는다. 향후 사용 예정인지, 계약에서 제거할지 확정 필요.

7. **`event/candidate` 구독이 서비스 문서에 없음.** 인터페이스 문서와 코드는 4개 토픽 구독이지만 서비스 문서 "Code Structure"는 3개 토픽만 기술한다(문서 누락으로 추정).
