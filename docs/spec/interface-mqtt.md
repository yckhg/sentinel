# MQTT 인터페이스 스펙

> 상태: 살아있는 계약 (living spec) · 독자: 스펙 작성자 / 오케스트레이터
>
> 이 문서는 Sentinel MQTT 접합부(seam)의 **계약 SSOT**다. 개별 서비스 스펙은 이 문서를 참조하며,
> 여기 정의된 토픽·페이로드·QoS·불변식을 벗어나는 발행/구독은 계약 위반이다.
> 인접 접면: 웹 API는 `docs/spec/interface-web-api.md`, 스트리밍은 `docs/spec/interface-streaming.md`.

## 목적 / 의도

- 산업 안전 현장의 H/W(ESP32 음성 인식 센서 등)와 Sentinel 서버가 주고받는 **모든 MQTT 메시지의 형식과 의미를 고정**한다.
- 발행자와 구독자가 서로의 내부 구현을 모른 채 이 문서만으로 통합할 수 있어야 한다 (펌웨어 개발자 = 이 문서만 읽으면 됨).
- 위급 알림(alert)은 유실·중복 없이 정확히 한 번 처리되고, 해소(resolved)는 웹과 현장 어느 쪽에서 풀든 전체 시스템이 동기화되는 것이 핵심 의도다.
- 계약 변경은 이 문서 수정 → 코드 반영 순서로만 진행한다 (문서가 코드를 따라가지 않는다).

## 언어 · 런타임

| 참여자 | 역할 | 언어/런타임 |
|--------|------|-------------|
| hw-gateway | Sentinel 측 유일한 MQTT 클라이언트 (구독 4토픽 + 발행 3토픽: `cmd/restart`, `alert/resolved`, 테스트 `alert`) | Go (paho.mqtt.golang), Docker 컨테이너 |
| H/W 디바이스 | 발행 최대 4토픽 (`alert`, `heartbeat`, `event/candidate` + optional `alert/resolved` — 현장 물리 버튼 탑재 시) + 구독 2토픽 | ESP32 펌웨어 (C, ESP-IDF) 등 — MQTT 3.1.1 클라이언트면 무엇이든 가능 |
| mosquitto | 브로커 | eclipse-mosquitto:2, Docker 컨테이너 |

- 페이로드는 전 토픽 공통 **UTF-8 JSON** 단일 오브젝트.
- 타임스탬프는 **ISO 8601 UTC** (예: `2026-04-24T02:40:03Z`).
- web-backend, notifier 등 다른 Sentinel 서비스는 MQTT에 직접 접속하지 않는다 — hw-gateway가 HTTP↔MQTT 브릿지 역할을 독점한다.

## 의존 도구 · 시스템

### 브로커 접속 계약

| 항목 | 내부 (Docker network `sentinel-net`) | 외부 (LAN/인터넷) |
|------|--------------------------------------|-------------------|
| Host | `mosquitto` | 공인 IP (라우터 포트포워딩) |
| Port | `1883` | `20011` |
| Scheme | `tcp://` (MQTT plain) | `tcp://` |
| 인증 | 없음 (no-auth 설정, anonymous 허용) | 없음 |
| TLS | 미사용 | 미사용 |

- H/W 디바이스 권장 옵션: 디바이스별 유일한 Client ID, clean session `true`, keep-alive `60s`, 자동 재연결(지수 백오프, max 60s).
- **Sentinel(hw-gateway)는 예외 — persistent session(clean session=`false`) + 고정 client ID `sentinel-hw-gateway`.** H/W 발행자에게 권장하는 `clean session=true`와 달리, gateway는 재연결 경계에서 브로커 세션 큐가 QoS1/2 미확인 메시지를 재전송하여 **수신측(gateway) 경보 유실을 방지**하기 위해 persistent session을 채택한다(설계자 승인). 이 보장은 브로커(mosquitto)의 세션 지속성 설정에 의존하며, 그 대가로 재연결 경계에서 QoS1 메시지의 **중복 수신**이 늘 수 있으므로 구독자(gateway) 처리는 idempotent해야 한다(`alert/resolved` 계약 §핵심 로직 참조). 상세는 `docs/spec/hw-gateway.md` §회복력·단언 S.
- **세션 지속성 (계약 산출물):** gateway의 persistent session 유실 방지 보장(§hw-gateway 단언 S)이 성립하려면, mosquitto는 gateway 오프라인 구간의 QoS1/2 in-flight 메시지를 세션 큐에 보관해야 한다. 최소 요건은 **브로커 상시 가동**이며(세션 큐는 메모리에 유지되므로 gateway만 재기동하는 시나리오는 이것으로 충분), 브로커 재기동에도 세션을 살리려면 mosquitto `persistence true` + persistence 볼륨 마운트를 함께 구성한다. 이 mosquitto 설정(persistence/볼륨)은 본 계약의 **산출물**이며, 미구성 시 단언 S는 실측 불가·미보장 상태다.
- ⚠️ 인증/화이트리스트가 없으므로 누구나 발행 가능 — 인터넷 노출 시 방화벽/포트포워딩 수준에서 접근 통제.

### 검증 도구

- `mosquitto_pub` / `mosquitto_sub` (mosquitto 컨테이너 내장: `docker compose exec mosquitto ...`)
- `docker compose logs hw-gateway` — 서버 측 수신/처리 확인
- **DB 판정 (incident/device 영속 확인):** 배포된 어떤 컨테이너에도 sqlite3 바이너리가 없다.
  DB 볼륨(`sentinel_db-data`)을 마운트한 일회성 컨테이너로 조회한다 (읽기 전용 — 실행 중인 서비스와 동시 조회 가능, WAL 호환):

  ```bash
  docker run --rm -v sentinel_db-data:/data alpine:3.19 \
    sh -c 'apk add -q sqlite && sqlite3 -readonly /data/sentinel.db "$1"' - "<SQL>"
  ```

  이하 검증 단언의 `db-query "<SQL>"`은 위 명령의 축약 표기다.

### 토픽 전체 지도

| 토픽 | 방향 | QoS | Retain | 용도 |
|------|------|-----|--------|------|
| `safety/{siteId}/alert` | H/W → Sentinel (+ Sentinel 자체 테스트 발행) | 2 | false | 위급 상황 알림 (실제 + 테스트) |
| `safety/{siteId}/heartbeat` | H/W → Sentinel | 0 | false | 장비 생존 신호 + 현재 상태 |
| `safety/{siteId}/event/candidate` | H/W → Sentinel | 0 | false | threshold 미달 위기 키워드 탐지 (참고용) |
| `safety/{siteId}/cmd/restart` | Sentinel → H/W | 1 | false | 원격 재시작 명령 |
| `safety/{siteId}/alert/resolved` | 양방향 | 1 | false | 위급 해소 통지 (웹 운영자 또는 현장 버튼) |

- `{siteId}`는 영숫자 식별자 (예: `site1`, `factory-a`).
- Sentinel(hw-gateway)은 `safety/+/alert`, `safety/+/heartbeat`, `safety/+/event/candidate`, `safety/+/alert/resolved` 4개를 와일드카드 구독한다.
- H/W는 자기 사이트의 `safety/{내siteId}/cmd/restart`, `safety/{내siteId}/alert/resolved` 2개를 구독한다.
- 전 토픽 retain `false` — 브로커에 마지막 메시지를 남기지 않는다. 상태는 heartbeat 반복 발행으로만 전달된다.
- **수신자 측 방어 (receiver-side defense):** 계약이 전 토픽 retain `false`를 보장하므로, hw-gateway는 `safety/#` 어느 토픽에서든 **retained 플래그가 설정된 메시지를 처리하지 않고 드롭한다** (경고 로그 `[RETAINED]`만 남김). retained 메시지는 정의상 계약 위반이거나 브로커에 잔존한 stale 메시지이므로 상태를 구동해서는 안 된다. 특히 `alert/resolved`에서 이 방어가 사람 게이트 원칙을 지킨다 — 잔존 retained resolve가 재구독 시 재전달되어 최근 미해결 incident를 사람 개입 없이 자동 해소하는 것을 차단한다.

> **운영 메모 (root cause):** 이 방어는 수신 측 안전장치일 뿐이며 근본 원인은 **발행자가 retain=1로 발행**하는 것이다 (예: VOICE-01 펌웨어가 `safety/site1/alert/resolved`에 retain=1 발행 → sentinel-voice 저장소에서 수정). 이미 브로커에 잔존 중인 retained 메시지는 별도로 비워야 한다: `mosquitto_pub -r -n -t safety/site1/alert/resolved` (빈 payload를 retain으로 발행해 retained 슬롯을 clear). 이는 브로커 상태를 바꾸는 mutating 작업이므로 운영자 승인 후 실행한다.

---

## 계약: `safety/{siteId}/alert` — 위급 알림

### 입력 (발행자 → 페이로드 스키마)

발행자는 두 종류다:

| 발행자 | 트리거 | QoS |
|--------|--------|-----|
| H/W 디바이스 | 위급 상황 감지 시 | 2 |
| Sentinel (hw-gateway) | 웹 운영자의 테스트 알림 요청 | 2 |

**H/W 발행 스키마 (실제 alert):**

| 필드 | 타입 | 필수 | 설명 |
|------|------|------|------|
| `deviceId` | string | ✅ | 디바이스 고유 ID (예: `PRESS-01`) |
| `siteId` | string | ✅ | 사이트 ID — 토픽의 `{siteId}`와 일치해야 함 |
| `type` | string | ✅ | 알림 유형 (예: `scream`, `help`, `auto_stop`, `gas_leak`, `voice_alert`) |
| `timestamp` | string | ✅ | ISO 8601 UTC — **최초 감지 시각. 재전송 시 변경 금지** |
| `alertId` | string | ✅ | alert 이벤트 고유 ID. 형식: `{deviceId}-{최초감지_timestamp}` (예: `VOICE-01-20260424T024003Z`). 재전송 시 동일 값 유지 |
| `description` | string | – | 사람이 읽을 설명 |
| `severity` | string | – | `critical` \| `warning` |
| `test` | bool | – | `true`이면 테스트 알림 (incident로 기록되되 TEST 표식) |

```json
{
  "deviceId": "VOICE-01",
  "siteId": "site1",
  "type": "voice_alert",
  "alertId": "VOICE-01-20260424T024003Z",
  "description": "살려주세요 감지됨 (신뢰도: 0.923)",
  "severity": "critical",
  "timestamp": "2026-04-24T02:40:03Z"
}
```

**Sentinel 테스트 alert 스키마:** hw-gateway가 테스트 요청을 받으면 같은 토픽에 QoS 2로 발행한다. 고정값:
`type: "test"`, `severity: "critical"`, `test: true`, `timestamp`는 발행 시각(서버 시각).
요청에 `siteId`/`deviceId`가 없으면 각각 `"test"`/`"TEST-DEVICE"`가 기본값이다.
**테스트 alert에는 `alertId`가 없다** — 따라서 dedup 대상에서 제외된다 (`alertId` 필수 규정은 H/W 발행자에게만 적용).

```json
{
  "deviceId": "TEST-DEVICE",
  "siteId": "test",
  "type": "test",
  "description": "[테스트] 비상 신호 시뮬레이션",
  "severity": "critical",
  "timestamp": "2026-07-02T10:00:00Z",
  "test": true
}
```

### 출력 (계약: 구독자가 기대할 수 있는 것)

구독자: hw-gateway (`safety/+/alert`, QoS 2). 유효한 alert 수신 시 발행자는 다음을 기대할 수 있다:

1. **incident 생성** — web-backend에 `{siteId, deviceId, alertId, description, occurredAt, isTest}` 기록 + WebSocket으로 웹 UI 실시간 푸시. `alertId`는 web-backend가 dedup 키로 사용한다(아래 4번 중복 무시의 근거). <!-- 정정(코드-실측): main.go IncidentPayload 가 AlertID 도 전송하며 web-backend incidents.go 가 이를 dedup 키로 사용. 이전 필드 목록은 alertId 누락. -->
2. **알림 채널 발송** — notifier가 전체 페이로드를 받아 알림 발송.
3. **device 자동 등록** — 처음 보는 `(siteId, deviceId)`는 devices 테이블에 자동 등록, 기존이면 `last_seen` 갱신, soft-delete 상태면 복원.
4. **중복 무시** — 동일 `alertId` 재수신 시 추가 incident를 생성하지 않는다 (MQTT ACK만 반환).

### 핵심 로직 (QoS, retain, 발행 주기/조건 등 불변식)

- **QoS 2 (exactly-once), retain false.** H/W는 반드시 QoS 2로 발행한다.
- **발행 조건:** 위급 상황 감지 시 즉시. 주기 발행 아님.
- **재전송 정책:** MQTT 재연결 후 미확인 alert는 재전송해야 하며, 이때 `alertId`·`timestamp`는 최초 감지 시점 값을 유지한다. 서버는 `alertId`로 dedup한다.
- **dedup은 in-memory** — hw-gateway 재시작 시 초기화되고, 24시간 후 만료된다. `alertId` 누락 시 incident는 생성되되 dedup은 건너뛴다.
- **alertId 부재 alert의 중복 안전성 (계약):** 서버 dedup은 `alertId`가 있는 alert에만 적용되므로, `alertId`가 없는 테스트 alert는 dedup 대상이 아니다. 이들의 중복 방지는 **토픽 QoS 2(exactly-once)**에 의존한다 — gateway가 persistent session이어도 QoS 2 흐름은 재연결 경계에서 정확히 한 번 전달되어 test alert가 중복 forward되지 않는다. (QoS 1 흐름인 `alert/resolved`는 재전송될 수 있어 별도 idempotency 계약을 둔다 — 해당 계약 참조.)
- **siteId 일관성:** 서버는 페이로드의 `siteId`를 토픽의 `{siteId}`로 덮어쓴다. 토픽이 진실이다.
- **필수 필드 누락 시:** `deviceId`/`siteId`/`type`/`timestamp` 중 하나라도 비어 있으면 서버는 메시지를 무시하고 경고 로그만 남긴다.
- **다운스트림 격리:** notifier 호출 실패는 incident 기록에 영향을 주지 않는다 (두 호출은 병렬, 각 10초 타임아웃).
- **Malformed JSON:** 무시 + 에러 로그. 발행자에게 별도 통지 없음.

### 검증 단언 (TDD)

**A-1. 유효 alert → incident 생성 (OK: incident row 존재)**
```bash
docker compose exec mosquitto mosquitto_pub -h localhost -q 2 -t 'safety/site1/alert' \
  -m '{"deviceId":"SPEC-01","siteId":"site1","type":"scream","alertId":"SPEC-01-A1","timestamp":"2026-07-02T10:00:00Z","severity":"critical"}'
# 판정:
db-query "SELECT COUNT(*) FROM incidents WHERE device_id='SPEC-01';"
# OK: 1 이상 · NOK: 0
```

**A-2. 동일 alertId 재전송 → incident 중복 없음 (OK: 카운트 불변)**
```bash
# A-1과 완전히 동일한 명령을 한 번 더 실행 후:
db-query "SELECT COUNT(*) FROM incidents WHERE device_id='SPEC-01';"
# OK: A-1 직후와 동일 값 (incident 미증가)
# NOK: 카운트 증가
```

**A-3. 필수 필드 누락 → 무시 (OK: incident 미생성)**
```bash
docker compose exec mosquitto mosquitto_pub -h localhost -q 2 -t 'safety/site1/alert' \
  -m '{"deviceId":"SPEC-02","siteId":"site1"}'
db-query "SELECT COUNT(*) FROM incidents WHERE device_id='SPEC-02';"
# OK: 0 (incident 미생성, hw-gateway 컨테이너 생존) · NOK: incident 생성됨
```

**A-4. Malformed JSON → 무시 (OK: 크래시 없음 + 후속 처리 정상)**
```bash
docker compose exec mosquitto mosquitto_pub -h localhost -q 2 -t 'safety/site1/alert' -m 'not-json{'
docker compose ps hw-gateway   # 컨테이너 running 유지 확인
# 이어서 A-1 유형의 유효 alert를 새 alertId로 발행 → incident 정상 생성 확인
# OK: hw-gateway 생존 + 후속 유효 alert 정상 처리 · NOK: crash 또는 후속 처리 실패
```

**A-5. siteId 덮어쓰기 (OK: incident의 site_id가 토픽 값)**
```bash
docker compose exec mosquitto mosquitto_pub -h localhost -q 2 -t 'safety/site1/alert' \
  -m '{"deviceId":"SPEC-03","siteId":"WRONG-SITE","type":"scream","alertId":"SPEC-03-A5","timestamp":"2026-07-02T10:05:00Z"}'
db-query "SELECT site_id FROM incidents WHERE device_id='SPEC-03' ORDER BY id DESC LIMIT 1;"
# OK: site1 · NOK: WRONG-SITE
```

---

## 계약: `safety/{siteId}/heartbeat` — 생존 신호

### 입력 (발행자 → 페이로드 스키마)

발행자: 각 H/W 디바이스, **주기 발행 (권장 10초)**.

| 필드 | 타입 | 필수 | 설명 |
|------|------|------|------|
| `deviceId` | string | ✅ | 디바이스 고유 ID — 누락 시 메시지 전체 무시 |
| `siteId` | string | ✅ | 사이트 ID — 누락 시 메시지 전체 무시 (값 자체는 토픽의 `{siteId}`로 덮어씀) |
| `timestamp` | string | – | ISO 8601 UTC — 송신 시각. 서버는 값을 검증/사용하지 않음 (수신 시각을 서버가 자체 기록) |
| `status` | string | – | `running` \| `stopped` \| `error` — 서버는 값을 검증하지 않음 (로그 기록만) |
| `alertState` | string | – | `none` \| `active` — 현재 alert 발동 여부. **누락/빈 값이면 서버가 `"none"`으로 보정** |

```json
{
  "deviceId": "VOICE-01",
  "siteId": "site1",
  "status": "running",
  "alertState": "active",
  "timestamp": "2026-04-24T02:40:20Z"
}
```

> 서버가 강제하는 필수 필드는 `deviceId`/`siteId` 둘뿐이다. `timestamp`/`status`/`alertState`는
> H/W 발행자 규율(권장)이며, 서버는 누락돼도 heartbeat를 처리한다.

### 출력 (계약: 구독자가 기대할 수 있는 것)

구독자: hw-gateway (`safety/+/heartbeat`, QoS 0). 발행자는 다음을 기대할 수 있다:

1. 장비 상태가 in-memory로 갱신되어 (`alive=true`, `lastHeartbeat`, `alertState`) 웹 장비 목록에 반영된다.
2. devices 테이블에 영속 등록/갱신/복원된다 (best-effort, 5초 타임아웃 — 실패해도 heartbeat 처리 자체는 계속).
3. `HEARTBEAT_TIMEOUT_SEC`(기본 30초) 동안 미수신 시 장비가 `alive=false`로 표시된다. DB의 soft-delete(`deleted_at`)와는 무관.

### 핵심 로직 (QoS, retain, 발행 주기/조건 등 불변식)

- **QoS 0 (at-most-once), retain false.** 한 개 유실되어도 무방한 설계 — 타임아웃(30초) > 발행 주기(10초) × 2 이므로 단발 유실이 alive 판정을 뒤집지 않는다.
- **발행 주기 10초 권장.** 주기를 늘리려면 `HEARTBEAT_TIMEOUT_SEC`과 함께 조정해야 한다 (주기 × 2 < 타임아웃 유지).
- **alert 발동 중에도 heartbeat는 계속 발행** — `alertState: "active"`로 경보 상태를 실어 나른다. 서버/웹은 heartbeat만으로 장비의 현재 경보 상태를 파악할 수 있어야 한다.
- **siteId 일관성:** 토픽의 `{siteId}`가 페이로드를 덮어쓴다.
- **incident를 만들지 않는다.** heartbeat는 상태 채널이지 이벤트 채널이 아니다.
- **restart 게이트(웹 계층):** 최초 heartbeat/alert/candidate 중 어느 하나라도 한 번 들어와 device가 devices 테이블에 등록되기 전까지, **웹 경로의 재시작 요청**(`docs/spec/interface-web-api.md`의 장비 재시작 API)은 400으로 거부된다 (자동 등록 트리거는 "Device ID 정책" 참조). 이 게이트는 web-backend 계층에만 존재한다 — `cmd/restart` 계약의 "핵심 로직" 참조.

### 검증 단언 (TDD)

**H-1. heartbeat → 장비 alive 등록 (OK: 장비 상태 API + devices 테이블에 표시)**
```bash
docker compose exec mosquitto mosquitto_pub -h localhost -q 0 -t 'safety/site1/heartbeat' \
  -m '{"deviceId":"SPEC-HB-01","siteId":"site1","status":"running","alertState":"none","timestamp":"2026-07-02T10:10:00Z"}'
# 판정 1 — 장비 상태 API (hw-gateway GET /api/equipment/status):
#   SPEC-HB-01 항목이 alive=true, alertState="none"으로 존재
# 판정 2 — 영속 등록:
db-query "SELECT COUNT(*) FROM devices WHERE device_id='SPEC-HB-01';"
# OK: API에 alive=true + devices 카운트 1 이상 · NOK: 둘 중 하나라도 없음
```

**H-2. 타임아웃 후 alive=false (OK: 30초+ 무발행 시 dead 마킹)**
```bash
# H-1 이후 heartbeat를 35초간 발행하지 않고 장비 상태 API(hw-gateway GET /api/equipment/status) 조회:
# OK: SPEC-HB-01의 alive=false, 단 devices row는 삭제되지 않음
# NOK: 여전히 alive=true 또는 row 삭제됨
```

**H-3. heartbeat는 incident를 만들지 않음 (OK: incidents 불변)**
```bash
db-query "SELECT COUNT(*) FROM incidents WHERE device_id='SPEC-HB-01';"
# OK: 0 · NOK: 1 이상
```

**H-4. alertState=active 전파 (OK: 서버가 경보 상태 인지)**
```bash
docker compose exec mosquitto mosquitto_pub -h localhost -q 0 -t 'safety/site1/heartbeat' \
  -m '{"deviceId":"SPEC-HB-01","siteId":"site1","status":"running","alertState":"active","timestamp":"2026-07-02T10:11:00Z"}'
# 판정 — 장비 상태 API(hw-gateway GET /api/equipment/status)에서 SPEC-HB-01 조회:
# OK: alertState="active" · NOK: alertState="none"으로 처리됨
```

**H-5. alertState 누락 → "none" 보정 (OK: 기본값 처리)**
```bash
docker compose exec mosquitto mosquitto_pub -h localhost -q 0 -t 'safety/site1/heartbeat' \
  -m '{"deviceId":"SPEC-HB-02","siteId":"site1","timestamp":"2026-07-02T10:12:00Z"}'
# 판정 — 장비 상태 API에서 SPEC-HB-02 조회:
# OK: alive=true + alertState="none" (누락에도 heartbeat 정상 처리) · NOK: 메시지 무시됨
```

---

## 계약: `safety/{siteId}/event/candidate` — threshold 미달 위기 키워드 탐지

### 입력 (발행자 → 페이로드 스키마)

발행자: H/W 디바이스. 위기 키워드로 분류됐지만 신뢰도가 alert threshold에 미달한 경우.

| 필드 | 타입 | 필수 | 설명 |
|------|------|------|------|
| `deviceId` | string | ✅ | 디바이스 고유 ID — 누락 시 메시지 전체 무시 |
| `siteId` | string | ✅ | 사이트 ID — 누락 시 메시지 전체 무시 |
| `class` | string | ✅ | 모델이 예측한 클래스 (예: `save_me`, `call_119`) — 누락 시 무시 |
| `confidence` | number | ✅ | 예측 신뢰도. **양수만 수용 (0 < confidence ≤ 1.0)** — `0.0` 이하는 거부(메시지 무시) |
| `threshold` | number | ✅ | 현재 alert 발동 기준 threshold. **양수만 수용** — `0.0` 이하는 거부 |
| `type` | string | – | `voice_candidate` 고정 (발행자 규율 — 서버는 값을 검증하지 않음) |
| `timestamp` | string | – | ISO 8601 UTC (발행자 규율 — 서버는 값을 검증/사용하지 않음) |

```json
{
  "deviceId": "VOICE-01",
  "siteId": "site1",
  "type": "voice_candidate",
  "class": "save_me",
  "confidence": 0.609,
  "threshold": 0.80,
  "timestamp": "2026-04-24T02:40:03Z"
}
```

### 출력 (계약: 구독자가 기대할 수 있는 것)

구독자: hw-gateway (`safety/+/event/candidate`, QoS 0). 발행자가 기대할 수 있는 것은 **최소한**이다:

1. 서버 로그 기록 (참고/통계 용도).
2. device 등록/`last_seen` 갱신 (best-effort).
3. **그 이상은 없다** — incident 생성 없음, notifier 호출 없음, GPIO/경보 동작 없음. 이 토픽은 alert가 아니다.

### 핵심 로직 (QoS, retain, 발행 주기/조건 등 불변식)

- **QoS 0, retain false.** 유실 허용 — 참고 정보.
- **발행 조건 (펌웨어):** `0.40 ≤ confidence < threshold` 구간에서만 발행. `confidence < 0.40`은 발행하지 않는다.
- **재전송 불필요:** MQTT 오프라인 중 발생한 candidate는 재연결 후 재전송하지 않는다 (best-effort 채널).
- **alert와의 배타성:** `confidence ≥ threshold`면 candidate가 아니라 `safety/{siteId}/alert`를 발행해야 한다. 같은 탐지가 두 토픽에 동시 발행되어서는 안 된다.

### 검증 단언 (TDD)

**C-1. 유효 candidate → device 등록만, incident 없음 (OK: devices 등록 + incident 0)**
```bash
docker compose exec mosquitto mosquitto_pub -h localhost -q 0 -t 'safety/site1/event/candidate' \
  -m '{"deviceId":"SPEC-CD-01","siteId":"site1","type":"voice_candidate","class":"save_me","confidence":0.61,"threshold":0.80,"timestamp":"2026-07-02T10:20:00Z"}'
db-query "SELECT (SELECT COUNT(*) FROM devices WHERE device_id='SPEC-CD-01'),
          (SELECT COUNT(*) FROM incidents WHERE device_id='SPEC-CD-01');"
# OK: devices 1 이상 AND incidents 0 · NOK: incident 생성됨 또는 미등록
```

**C-2. candidate로 device 등록 (OK: devices에 row 생성/갱신)**
```bash
# C-1 이후 web-backend 장비 API 또는 devices 테이블에서 SPEC-CD-01 확인
# OK: row 존재 · NOK: 미등록
```

**C-3. 핵심 필드 누락 → 무시 (OK: 처리 skip — device 미등록)**
```bash
docker compose exec mosquitto mosquitto_pub -h localhost -q 0 -t 'safety/site1/event/candidate' \
  -m '{"deviceId":"SPEC-CD-02","siteId":"site1"}'
db-query "SELECT COUNT(*) FROM devices WHERE device_id='SPEC-CD-02';"
# OK: 0 (미등록 — 메시지 무시됨) · NOK: 등록됨 (정상 처리됨)
```

**C-4. confidence=0.0 → 거부 (OK: device 미등록)**
```bash
docker compose exec mosquitto mosquitto_pub -h localhost -q 0 -t 'safety/site1/event/candidate' \
  -m '{"deviceId":"SPEC-CD-03","siteId":"site1","class":"save_me","confidence":0.0,"threshold":0.80}'
db-query "SELECT COUNT(*) FROM devices WHERE device_id='SPEC-CD-03';"
# OK: 0 (0 이하 confidence는 거부) · NOK: 등록됨
```

---

## 계약: `safety/{siteId}/cmd/restart` — 원격 재시작 명령

### 입력 (발행자 → 페이로드 스키마)

발행자: Sentinel (hw-gateway) — 웹 운영자의 재시작 요청을 MQTT로 변환해 발행.

| 필드 | 타입 | 필수 | 설명 |
|------|------|------|------|
| `deviceId` | string | ✅ | 대상 디바이스 ID |
| `siteId` | string | ✅ | 사이트 ID |
| `requestedBy` | string | ✅ | 명령을 내린 사용자명 |
| `reason` | string | – | 재시작 사유 |
| `timestamp` | string | ✅ | ISO 8601 UTC |

```json
{
  "deviceId": "PRESS-01",
  "siteId": "site1",
  "requestedBy": "admin",
  "reason": "Crisis resolved",
  "timestamp": "2026-04-13T09:30:00Z"
}
```

### 출력 (계약: 구독자가 기대할 수 있는 것)

구독자: H/W 디바이스 (`safety/{내siteId}/cmd/restart`, QoS 1). Sentinel이 기대할 수 있는 것:

1. 페이로드의 `deviceId`가 자기 자신인 디바이스**만** 재시작을 수행한다.
2. `deviceId`가 다른 디바이스는 메시지를 무시한다 (같은 사이트의 모든 구독 장비가 수신하므로 필수 가드).
3. 재시작 후 디바이스는 heartbeat 발행을 재개한다 (재시작 성공의 관측 가능한 신호).

### 핵심 로직 (QoS, retain, 발행 주기/조건 등 불변식)

- **QoS 1 (at-least-once), retain false.** 중복 수신 가능 — 재시작 동작은 idempotent해야 한다 (이미 재시작 중이면 무시).
- **발행 조건:** 웹 운영자의 명시적 요청 시에만. 자동 발행 없음.
- **등록 게이트는 web-backend 계층에만 존재한다.** 웹 경로의 재시작 요청(`docs/spec/interface-web-api.md`의 장비 재시작 API)은 devices 테이블에 등록되고 미삭제 상태인 device만 통과시키며, 미등록/삭제된 device는 400으로 거부한다. **hw-gateway의 발행 계층은 이 게이트를 갖지 않는다** — `siteId`/`deviceId`가 비어 있지 않으면 미등록 device로도 그대로 MQTT 발행된다. 따라서 등록 게이트가 필요한 호출자는 반드시 웹 경로를 경유해야 한다.
- **용도 제한:** 펌웨어 hang, 센서 오작동 등 **비정상 복구 전용**. 정상 alert 해소 경로는 `alert/resolved`이며 restart를 수반하지 않는다.

### 검증 단언 (TDD)

**R-1. 발행 형식 준수 (OK: 구독자 관점에서 페이로드 완전성)**
```bash
# 터미널 1 — 구독 대기:
docker compose exec mosquitto mosquitto_sub -v -q 1 -t 'safety/site1/cmd/restart'
# 터미널 2 — 웹 UI 또는 web-backend API로 site1의 등록된 장비 재시작 요청
# OK: deviceId/siteId/requestedBy/timestamp가 모두 채워진 JSON 1건 수신
# NOK: 미수신 또는 필수 필드 빈 값
```

**R-2. 미등록 device 거부 — 웹 경로 (OK: MQTT 발행 자체가 일어나지 않음)**
```bash
# 터미널 1 — 구독 대기 (R-1과 동일)
# 터미널 2 — heartbeat를 한 번도 보낸 적 없는 deviceId로
#            web-backend 장비 재시작 API(docs/spec/interface-web-api.md) 호출
# OK: API가 400 반환 AND 구독 터미널에 아무것도 수신되지 않음
# NOK: 메시지 발행됨
# (주의: 이 게이트는 웹 경로 전용 — hw-gateway 내부 API 직접 호출은 게이트를 거치지 않는다)
```

---

## 계약: `safety/{siteId}/alert/resolved` — 위급 해소 (양방향)

### 입력 (발행자 → 페이로드 스키마)

발행자는 두 종류다. **누가 발행하든 같은 스키마, 같은 토픽** — 모든 구독자가 동일하게 동기화된다.

| 시나리오 | 발행자 | 트리거 |
|----------|--------|--------|
| 웹에서 운영자가 해제 | Sentinel (hw-gateway) | 웹 resolve → incident 갱신 후 MQTT 발행 |
| 현장 물리 버튼으로 해제 | H/W 디바이스 | reset/해제 버튼 입력 |

| 필드 | 타입 | 필수 | 설명 |
|------|------|------|------|
| `incidentId` | number | ✅ | 해소 대상 incident ID. 모르면 `0` 허용 (서버가 해당 site의 가장 최근 미해결 incident 매칭) |
| `siteId` | string | ✅ | 사이트 ID (토픽의 `{siteId}`와 일치) |
| `resolvedAt` | string | ✅ | ISO 8601 UTC |
| `resolvedBy` | object | ✅ | 해제 주체 — 아래 구조 |
| `originalAlert` | object | – | 원래 alert의 `type`/`deviceId` — 구독자의 선택적 반응용 |

`resolvedBy`:

| 필드 | 타입 | 설명 |
|------|------|------|
| `kind` | string | `"web"` \| `"sensor_button"` |
| `id` | string | web이면 사용자명, sensor_button이면 deviceId |
| `label` | string | 사람이 읽는 표시명 (UI/디스플레이용) |

```json
{
  "incidentId": 12345,
  "siteId": "site1",
  "resolvedAt": "2026-04-13T10:30:00Z",
  "resolvedBy": { "kind": "sensor_button", "id": "VOICE-01", "label": "VOICE-01 reset 버튼" },
  "originalAlert": { "type": "scream", "deviceId": "PRESS-01" }
}
```

### 출력 (계약: 구독자가 기대할 수 있는 것)

**구독자 1 — Sentinel (hw-gateway, `safety/+/alert/resolved`):**

- `resolvedBy.kind == "web"` → 자기 echo로 판단, 무시 (no-op).
- `resolvedBy.kind == "sensor_button"` → 해당 incident를 해소 처리 (DB 갱신 + 웹 UI 실시간 반영). `incidentId == 0`이면 해당 site의 가장 최근 미해결 incident를 매칭.
- 그 외 `kind` 값 → 무시 + 경고 로그.

**구독자 2 — H/W 디바이스 (`safety/{내siteId}/alert/resolved`), 수신 시 4단계 필수 동작:**

1. **LED/부저 OFF** (수신 100ms 이내) — 모든 경보 출력 즉시 차단.
2. **내부 alert/latched 플래그 clear** — 상태 머신을 idle로.
3. **감지 루틴 resume** — alert 중 정지했던 분류기/센서 polling 재개.
4. **해제 주체 표시** — `resolvedBy.label`을 디스플레이/로그에 출력.

4단계 이후 디바이스는 alert 발생 전과 **동등한 상태**여야 하며, 별도 restart 없이 다음 alert를 즉시 감지/발행할 수 있어야 한다. 각 단계는 **idempotent** — 이미 해제된 상태에서 재수신해도 에러 없이 no-op.

### 핵심 로직 (QoS, retain, 발행 주기/조건 등 불변식)

- **QoS 1 (at-least-once), retain false.** 중복 수신 전제 → 모든 구독자 동작이 idempotent해야 한다.
- **재연결 중복과 idempotency (계약):** gateway는 persistent session으로 접속하므로(§브로커 접속 계약), 재연결 경계에서 브로커 세션 큐가 이 QoS1 메시지를 재전송할 수 있다. 따라서 sensor_button resolve의 다운스트림 forward(`resolve-from-sensor`)는 **idempotent**해야 한다 — 이미 해소된 incident를 재수신하면 no-op으로 처리하고 중복 해소·중복 부수효과를 만들지 않는다. `incidentId==0` fallback도 이미 해소된 대상에는 재적용하지 않는다. web-kind echo는 무시되므로 재전송돼도 무해하다.
- **사람 게이트 원칙:** alert는 자동 해제되지 않는다. 반드시 사람(웹 클릭 또는 물리 버튼)이 트리거한다.
- **Echo 가드:** 브로커는 QoS 1에서 자기 발행 메시지를 자신에게 echo한다. 발행자는 `resolvedBy.kind`(서버: `"web"` 무시) 또는 `resolvedBy.id == 내 deviceId`(펌웨어)로 자기 echo를 무시한다.
- **디바이스 측 발행은 optional, 구독+4단계는 필수.** 버튼 없는 디바이스도 수신 동작은 구현해야 한다.
- **다중 incident:** 같은 site에 동시 다발 incident 가능 — `incidentId`로 개별 매칭, `0`은 최근 미해결 fallback.
- **restart와의 분리:** 정상 resolve 흐름에 `cmd/restart`는 개입하지 않는다 (감지 resume이 4단계에 포함되므로).
- **siteId 일관성:** 서버는 토픽의 `{siteId}`로 페이로드를 덮어쓴다.

### 검증 단언 (TDD)

**RS-1. 웹 해제 → MQTT 발행 (OK: 구독자가 kind=web 메시지 수신)**
```bash
# 터미널 1:
docker compose exec mosquitto mosquitto_sub -v -q 1 -t 'safety/+/alert/resolved'
# 터미널 2 — 웹 UI에서 미해결 incident 하나를 resolve
# OK: {"incidentId":N,...,"resolvedBy":{"kind":"web",...}} 1건 수신
# NOK: 미수신
```

**RS-2. 센서 버튼 해제 → incident 해소 (OK: DB resolved_at 갱신)**
```bash
# 사전: A-1 유형으로 미해결 incident 생성 후 그 id를 확인.
docker compose exec mosquitto mosquitto_pub -h localhost -q 1 -t 'safety/site1/alert/resolved' \
  -m '{"incidentId":0,"siteId":"site1","resolvedAt":"2026-07-02T10:30:00Z","resolvedBy":{"kind":"sensor_button","id":"SPEC-01","label":"SPEC-01 reset 버튼"}}'
db-query "SELECT resolved_at FROM incidents WHERE site_id='site1' ORDER BY id DESC LIMIT 1;"
# OK: resolved_at NOT NULL · NOK: NULL 유지
```

**RS-3. 서버 echo 무시 (OK: web-kind 메시지가 incident 재처리를 유발하지 않음)**
```bash
# 사전: A-1 유형으로 미해결 incident를 하나 만들어 둔다 (resolved_at IS NULL).
docker compose exec mosquitto mosquitto_pub -h localhost -q 1 -t 'safety/site1/alert/resolved' \
  -m '{"incidentId":0,"siteId":"site1","resolvedAt":"2026-07-02T10:31:00Z","resolvedBy":{"kind":"web","id":"admin","label":"관리자"}}'
db-query "SELECT resolved_at FROM incidents WHERE site_id='site1' ORDER BY id DESC LIMIT 1;"
# OK: resolved_at NULL 유지 (echo가 resolve-from-sensor 경로를 타지 않음)
# NOK: resolved_at 갱신됨
```

**RS-4. 알 수 없는 kind 무시 (OK: incident 상태 불변)**
```bash
# 사전: 미해결 incident 존재 (RS-3과 동일 조건).
docker compose exec mosquitto mosquitto_pub -h localhost -q 1 -t 'safety/site1/alert/resolved' \
  -m '{"incidentId":0,"siteId":"site1","resolvedAt":"2026-07-02T10:32:00Z","resolvedBy":{"kind":"alien","id":"x","label":"x"}}'
db-query "SELECT resolved_at FROM incidents WHERE site_id='site1' ORDER BY id DESC LIMIT 1;"
# OK: resolved_at NULL 유지 (no-op) · NOK: incident 상태 변경됨
```

**RS-5. (펌웨어) 4단계 idempotency (OK: 동일 메시지 2회 수신에도 정상)**
```bash
# RS-2의 mosquitto_pub을 연속 2회 실행 (QoS 1 중복 시뮬레이션).
# 대상 디바이스 관측:
# OK: LED/부저 OFF 유지, 에러 로그 없음, 이후 새 alert 감지/발행 가능
# NOK: GPIO 에러, 상태 머신 stuck, 감지 루틴 중복 기동
```

**RS-6. retained resolve 드롭 (OK: retain 메시지가 incident를 자동 해소하지 않음)**
```bash
# 사전: A-1 유형으로 미해결 incident를 하나 만들어 둔다 (resolved_at IS NULL).
# 계약 위반을 시뮬레이션: -r 플래그로 retain=1 sensor_button resolve 발행.
docker compose exec mosquitto mosquitto_pub -h localhost -q 1 -r -t 'safety/site1/alert/resolved' \
  -m '{"incidentId":0,"siteId":"site1","resolvedAt":"2026-07-02T10:33:00Z","resolvedBy":{"kind":"sensor_button","id":"SPEC-01","label":"SPEC-01 reset 버튼"}}'
db-query "SELECT resolved_at FROM incidents WHERE site_id='site1' ORDER BY id DESC LIMIT 1;"
# OK: resolved_at NULL 유지 + hw-gateway 로그에 [RETAINED] 경고 (retained 메시지 드롭)
# NOK: resolved_at 갱신됨 (retained 메시지가 자동 해소를 구동)
# 정리(운영 승인 필요): mosquitto_pub -r -n -t safety/site1/alert/resolved 로 잔존 retained 슬롯 clear
```

---

## Device ID 정책 (전 토픽 공통 불변식)

- `deviceId`는 펌웨어 하드코딩 또는 시리얼/MAC 기반으로 **재부팅 간 안정적**이어야 한다. 재부팅마다 바뀌면 새 device로 등록된다.
- `siteId`는 빌드 시 사이트별로 굽거나 설정으로 주입 — 역시 안정적이어야 한다.
- 자동 등록: heartbeat/alert/candidate 어느 것이든 처음 보는 `(siteId, deviceId)`는 자동 등록된다. 사전 프로비저닝 불필요.
- 영속: 등록된 device는 운영자가 명시적으로 삭제(soft delete)하지 않는 한 영속. 삭제된 device가 다시 신호를 보내면 자동 복원.

## 에러 정책 요약 (Sentinel 측)

| 시나리오 | 동작 |
|----------|------|
| Malformed JSON | 무시 + 에러 로그 |
| 필수 필드 누락 | 무시 + 경고 로그 |
| notifier 호출 실패 | 에러 로그, incident 기록은 계속 |
| web-backend 호출 실패 (incident forward) | **transport 에러(연결 실패/타임아웃) 및 HTTP 5xx** 최대 3회 재시도 (지수 백오프 + jitter, base 1s). **4xx는 클라이언트 에러로 재시도하지 않음.** 2xx일 때만 수락(생성 201 또는 dedup 200)으로 판정 <!-- 정정(코드-실측 타이브레이크, 근거 main.go:447-511): `if status < 500 { return 2xx }` — 5xx는 return 하지 않고 retry/backoff 루프로 폴스루하여 최대 3회 재시도한다. 이전 표기 "HTTP 응답을 받으면 4xx/5xx 무관 재시도 안 함"은 코드와 정면 모순(stale). web-backend alertId dedup(incidents.go)이 5xx 재시도의 중복 incident 생성을 막으므로 코드 자체는 안전. --> |
| device 등록 호출 실패 | 무시 (best-effort, 재시도 없음) |
| Broker 끊김 | 자동 재연결 (지수 백오프, max 60s) |

---

## ⚠️ 리뷰 필요 (의도 논의 대상)

본문은 코드 실측을 계약으로 기술한다. 아래는 실측과 별개로 **의도가 맞는지** 사람 리뷰가 필요한 항목.

1. **candidate만 siteId 토픽 덮어쓰기가 없다.** alert/heartbeat/alert-resolved는 토픽의 `{siteId}`로 페이로드를 덮어쓰지만, candidate 처리만 페이로드의 `siteId` 값을 그대로 사용한다. 토픽-페이로드 불일치 시 candidate만 다르게 동작 — "토픽이 진실" 원칙을 candidate에도 적용할지 결정 필요.

2. **alert `timestamp`의 관용 처리.** 본문 계약상 `timestamp`는 필수이며 누락 시 메시지가 무시되지만, 값이 **존재하되 malformed**인 경우는 거부되지 않는다: Unix epoch 정수 문자열은 수용되고, 파싱 불가/비정상 값은 서버 수신 시각으로 대체되어 incident가 생성된다. 이 관용이 의도인지(펌웨어 시계 불신 전제) 리뷰 필요.

3. **heartbeat `alertId`는 죽은 계약이다.** 과거 스키마에 있던 optional `alertId`(발동 중 alert의 ID를 heartbeat에 싣는 필드)는 서버가 수신 구조체에 담기만 하고 어디에도 사용하지 않으며, 웹/알림 등 어떤 소비자도 참조하지 않는다. 본문 스키마에서 제외했다. "heartbeat만으로 발동 중인 alert를 식별한다"는 원래 의도를 살릴 것인지(서버 측 소비 구현), 필드를 영구 폐기할 것인지 결정 필요.

4. **테스트 alert의 dedup 부재.** 테스트 alert는 `alertId` 없이 발행되므로 dedup이 적용되지 않는다 (본문 계약에 실측대로 기술). 테스트 요청 연타 시 incident가 요청 수만큼 생성된다 — 테스트 alert에도 식별자를 부여할지 리뷰 필요.
