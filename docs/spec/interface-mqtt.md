# MQTT 인터페이스 스펙

> 상태: 살아있는 계약 (living spec) · 독자: 스펙 작성자 / 오케스트레이터
>
> 이 문서는 Sentinel MQTT 접합부(seam)의 **계약 SSOT**다. 개별 서비스 스펙은 이 문서를 참조하며,
> 여기 정의된 토픽·페이로드·QoS·불변식을 벗어나는 발행/구독은 계약 위반이다.
> 기존 `docs/interfaces/mqtt-publisher-guide.md`를 "계약 + 검증 단언" 형태로 승격한 문서다.

## 목적 / 의도

- 산업 안전 현장의 H/W(ESP32 음성 인식 센서 등)와 Sentinel 서버가 주고받는 **모든 MQTT 메시지의 형식과 의미를 고정**한다.
- 발행자와 구독자가 서로의 내부 구현을 모른 채 이 문서만으로 통합할 수 있어야 한다 (펌웨어 개발자 = 이 문서만 읽으면 됨).
- 위급 알림(alert)은 유실·중복 없이 정확히 한 번 처리되고, 해소(resolved)는 웹과 현장 어느 쪽에서 풀든 전체 시스템이 동기화되는 것이 핵심 의도다.
- 계약 변경은 이 문서 수정 → 코드 반영 순서로만 진행한다 (문서가 코드를 따라가지 않는다).

## 언어 · 런타임

| 참여자 | 역할 | 언어/런타임 |
|--------|------|-------------|
| hw-gateway | Sentinel 측 유일한 MQTT 클라이언트 (구독 4토픽 + 발행 2토픽) | Go (paho.mqtt.golang), Docker 컨테이너 |
| H/W 디바이스 | 발행 3토픽 + 구독 2토픽 | ESP32 펌웨어 (C, ESP-IDF) 등 — MQTT 3.1.1 클라이언트면 무엇이든 가능 |
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

- 클라이언트 권장 옵션: 디바이스별 유일한 Client ID, clean session `true`, keep-alive `60s`, 자동 재연결(지수 백오프, max 60s).
- Sentinel 측 클라이언트 ID는 `sentinel-hw-gateway`로 고정.
- ⚠️ 인증/화이트리스트가 없으므로 누구나 발행 가능 — 인터넷 노출 시 방화벽/포트포워딩 수준에서 접근 통제.

### 검증 도구

- `mosquitto_pub` / `mosquitto_sub` (mosquitto 컨테이너 내장: `docker compose exec mosquitto ...`)
- `docker compose logs hw-gateway` — 서버 측 수신/처리 확인
- `sqlite3 /data/sentinel.db` (web-backend 컨테이너) — incident 영속 확인

### 토픽 전체 지도

| 토픽 | 방향 | QoS | Retain | 용도 |
|------|------|-----|--------|------|
| `safety/{siteId}/alert` | H/W → Sentinel | 2 | false | 위급 상황 알림 |
| `safety/{siteId}/heartbeat` | H/W → Sentinel | 0 | false | 장비 생존 신호 + 현재 상태 |
| `safety/{siteId}/event/candidate` | H/W → Sentinel | 0 | false | threshold 미달 위기 키워드 탐지 (참고용) |
| `safety/{siteId}/cmd/restart` | Sentinel → H/W | 1 | false | 원격 재시작 명령 |
| `safety/{siteId}/alert/resolved` | 양방향 | 1 | false | 위급 해소 통지 (웹 운영자 또는 현장 버튼) |

- `{siteId}`는 영숫자 식별자 (예: `site1`, `factory-a`).
- Sentinel(hw-gateway)은 `safety/+/alert`, `safety/+/heartbeat`, `safety/+/event/candidate`, `safety/+/alert/resolved` 4개를 와일드카드 구독한다.
- H/W는 자기 사이트의 `safety/{내siteId}/cmd/restart`, `safety/{내siteId}/alert/resolved` 2개를 구독한다.
- 전 토픽 retain `false` — 브로커에 마지막 메시지를 남기지 않는다. 상태는 heartbeat 반복 발행으로만 전달된다.

---

## 계약: `safety/{siteId}/alert` — 위급 알림

### 입력 (발행자 → 페이로드 스키마)

발행자: H/W 디바이스 (위급 상황 감지 시).

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

### 출력 (계약: 구독자가 기대할 수 있는 것)

구독자: hw-gateway (`safety/+/alert`, QoS 2). 유효한 alert 수신 시 발행자는 다음을 기대할 수 있다:

1. **incident 생성** — web-backend에 `{siteId, deviceId, description, occurredAt, isTest}` 기록 + WebSocket으로 웹 UI 실시간 푸시.
2. **알림 채널 발송** — notifier가 전체 페이로드를 받아 알림 발송.
3. **device 자동 등록** — 처음 보는 `(siteId, deviceId)`는 devices 테이블에 자동 등록, 기존이면 `last_seen` 갱신, soft-delete 상태면 복원.
4. **중복 무시** — 동일 `alertId` 재수신 시 추가 incident를 생성하지 않는다 (MQTT ACK만 반환).

### 핵심 로직 (QoS, retain, 발행 주기/조건 등 불변식)

- **QoS 2 (exactly-once), retain false.** H/W는 반드시 QoS 2로 발행한다.
- **발행 조건:** 위급 상황 감지 시 즉시. 주기 발행 아님.
- **재전송 정책:** MQTT 재연결 후 미확인 alert는 재전송해야 하며, 이때 `alertId`·`timestamp`는 최초 감지 시점 값을 유지한다. 서버는 `alertId`로 dedup한다.
- **dedup은 in-memory** — hw-gateway 재시작 시 초기화되고, 24시간 후 만료된다. `alertId` 누락 시 incident는 생성되되 dedup은 건너뛴다.
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
docker compose exec web-backend sqlite3 /data/sentinel.db \
  "SELECT COUNT(*) FROM incidents WHERE device_id='SPEC-01';"
# OK: 1 이상 · NOK: 0
```

**A-2. 동일 alertId 재전송 → incident 중복 없음 (OK: 카운트 불변)**
```bash
# A-1과 완전히 동일한 명령을 한 번 더 실행 후:
docker compose exec web-backend sqlite3 /data/sentinel.db \
  "SELECT COUNT(*) FROM incidents WHERE device_id='SPEC-01';"
# OK: A-1 직후와 동일 값 + hw-gateway 로그에 "Duplicate alertId=SPEC-01-A1, skipping"
# NOK: 카운트 증가
```

**A-3. 필수 필드 누락 → 무시 (OK: incident 미생성 + 경고 로그)**
```bash
docker compose exec mosquitto mosquitto_pub -h localhost -q 2 -t 'safety/site1/alert' \
  -m '{"deviceId":"SPEC-02","siteId":"site1"}'
docker compose logs --since 1m hw-gateway | grep "Missing required fields in alert"
# OK: 로그 라인 존재 + SPEC-02 incident 0건 · NOK: incident 생성됨
```

**A-4. Malformed JSON → 무시 (OK: 에러 로그만)**
```bash
docker compose exec mosquitto mosquitto_pub -h localhost -q 2 -t 'safety/site1/alert' -m 'not-json{'
docker compose logs --since 1m hw-gateway | grep "Malformed JSON"
# OK: 로그 라인 존재, hw-gateway 프로세스 생존 · NOK: 로그 없음 또는 crash
```

**A-5. siteId 덮어쓰기 (OK: incident의 site_id가 토픽 값)**
```bash
docker compose exec mosquitto mosquitto_pub -h localhost -q 2 -t 'safety/site1/alert' \
  -m '{"deviceId":"SPEC-03","siteId":"WRONG-SITE","type":"scream","alertId":"SPEC-03-A5","timestamp":"2026-07-02T10:05:00Z"}'
docker compose exec web-backend sqlite3 /data/sentinel.db \
  "SELECT site_id FROM incidents WHERE device_id='SPEC-03' ORDER BY id DESC LIMIT 1;"
# OK: site1 · NOK: WRONG-SITE
```

---

## 계약: `safety/{siteId}/heartbeat` — 생존 신호

### 입력 (발행자 → 페이로드 스키마)

발행자: 각 H/W 디바이스, **주기 발행 (권장 10초)**.

| 필드 | 타입 | 필수 | 설명 |
|------|------|------|------|
| `deviceId` | string | ✅ | 디바이스 고유 ID |
| `siteId` | string | ✅ | 사이트 ID |
| `timestamp` | string | ✅ | ISO 8601 UTC — 송신 시각 |
| `status` | string | ✅ | `running` \| `stopped` \| `error` |
| `alertState` | string | ✅ | `none` \| `active` — 현재 alert 발동 여부 |
| `alertId` | string | – | `alertState == "active"`일 때 발동 중인 alert의 ID (alert 토픽의 `alertId`와 동일값) |

```json
{
  "deviceId": "VOICE-01",
  "siteId": "site1",
  "status": "running",
  "alertState": "active",
  "alertId": "VOICE-01-20260424T024003Z",
  "timestamp": "2026-04-24T02:40:20Z"
}
```

### 출력 (계약: 구독자가 기대할 수 있는 것)

구독자: hw-gateway (`safety/+/heartbeat`, QoS 0). 발행자는 다음을 기대할 수 있다:

1. 장비 상태가 in-memory로 갱신되어 (`alive=true`, `lastHeartbeat`, `alertState`) 웹 장비 목록에 반영된다.
2. devices 테이블에 영속 등록/갱신/복원된다 (best-effort, 5초 타임아웃 — 실패해도 heartbeat 처리 자체는 계속).
3. `HEARTBEAT_TIMEOUT_SEC`(기본 30초) 동안 미수신 시 장비가 `alive=false`로 표시된다. DB의 soft-delete(`deleted_at`)와는 무관.

### 핵심 로직 (QoS, retain, 발행 주기/조건 등 불변식)

- **QoS 0 (at-most-once), retain false.** 한 개 유실되어도 무방한 설계 — 타임아웃(30초) > 발행 주기(10초) × 2 이므로 단발 유실이 alive 판정을 뒤집지 않는다.
- **발행 주기 10초 권장.** 주기를 늘리려면 `HEARTBEAT_TIMEOUT_SEC`과 함께 조정해야 한다 (주기 × 2 < 타임아웃 유지).
- **alert 발동 중에도 heartbeat는 계속 발행** — `alertState: "active"` + `alertId`로 경보 상태를 실어 나른다. 서버/웹은 heartbeat만으로 장비의 현재 경보 상태를 파악할 수 있어야 한다.
- **siteId 일관성:** 토픽의 `{siteId}`가 페이로드를 덮어쓴다.
- **incident를 만들지 않는다.** heartbeat는 상태 채널이지 이벤트 채널이 아니다.
- **restart 게이트:** 최초 heartbeat(또는 alert)가 한 번 들어와 device가 등록되기 전까지 해당 장비로의 `cmd/restart` 발행은 거부된다.

### 검증 단언 (TDD)

**H-1. heartbeat → 장비 alive 등록 (OK: 장비 목록에 표시)**
```bash
docker compose exec mosquitto mosquitto_pub -h localhost -q 0 -t 'safety/site1/heartbeat' \
  -m '{"deviceId":"SPEC-HB-01","siteId":"site1","status":"running","alertState":"none","timestamp":"2026-07-02T10:10:00Z"}'
docker compose logs --since 1m hw-gateway | grep "HEARTBEAT.*SPEC-HB-01"
# OK: "deviceId=SPEC-HB-01 ... alertState=none" 로그 + devices 테이블에 row 생성
# NOK: 로그 없음 또는 row 없음
```

**H-2. 타임아웃 후 alive=false (OK: 30초+ 무발행 시 dead 마킹)**
```bash
# H-1 이후 heartbeat를 35초간 발행하지 않고 web-backend 장비 API 조회:
# OK: SPEC-HB-01의 alive=false, 단 devices row는 삭제되지 않음
# NOK: 여전히 alive=true 또는 row 삭제됨
```

**H-3. heartbeat는 incident를 만들지 않음 (OK: incidents 불변)**
```bash
docker compose exec web-backend sqlite3 /data/sentinel.db \
  "SELECT COUNT(*) FROM incidents WHERE device_id='SPEC-HB-01';"
# OK: 0 · NOK: 1 이상
```

**H-4. alertState=active 전파 (OK: 서버가 경보 상태 인지)**
```bash
docker compose exec mosquitto mosquitto_pub -h localhost -q 0 -t 'safety/site1/heartbeat' \
  -m '{"deviceId":"SPEC-HB-01","siteId":"site1","status":"running","alertState":"active","alertId":"SPEC-HB-01-X","timestamp":"2026-07-02T10:11:00Z"}'
docker compose logs --since 1m hw-gateway | grep "SPEC-HB-01.*alertState=active"
# OK: 로그 존재 · NOK: alertState가 none으로 처리됨
```

---

## 계약: `safety/{siteId}/event/candidate` — threshold 미달 위기 키워드 탐지

### 입력 (발행자 → 페이로드 스키마)

발행자: H/W 디바이스. 위기 키워드로 분류됐지만 신뢰도가 alert threshold에 미달한 경우.

| 필드 | 타입 | 필수 | 설명 |
|------|------|------|------|
| `deviceId` | string | ✅ | 디바이스 고유 ID |
| `siteId` | string | ✅ | 사이트 ID |
| `type` | string | ✅ | `voice_candidate` 고정 |
| `class` | string | ✅ | 모델이 예측한 클래스 (예: `save_me`, `call_119`) |
| `confidence` | number | ✅ | 예측 신뢰도 (0.0 ~ 1.0) |
| `threshold` | number | ✅ | 현재 alert 발동 기준 threshold |
| `timestamp` | string | ✅ | ISO 8601 UTC |

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

**C-1. 유효 candidate → 로그만, incident 없음 (OK: 로그 존재 + incident 0)**
```bash
docker compose exec mosquitto mosquitto_pub -h localhost -q 0 -t 'safety/site1/event/candidate' \
  -m '{"deviceId":"SPEC-CD-01","siteId":"site1","type":"voice_candidate","class":"save_me","confidence":0.61,"threshold":0.80,"timestamp":"2026-07-02T10:20:00Z"}'
docker compose logs --since 1m hw-gateway | grep "CANDIDATE.*SPEC-CD-01"
docker compose exec web-backend sqlite3 /data/sentinel.db \
  "SELECT COUNT(*) FROM incidents WHERE device_id='SPEC-CD-01';"
# OK: 로그 존재 AND 카운트 0 · NOK: incident 생성됨
```

**C-2. candidate로 device 등록 (OK: devices에 row 생성/갱신)**
```bash
# C-1 이후 web-backend 장비 API 또는 devices 테이블에서 SPEC-CD-01 확인
# OK: row 존재 · NOK: 미등록
```

**C-3. 핵심 필드 누락 → 무시 (OK: 경고 로그, 처리 skip)**
```bash
docker compose exec mosquitto mosquitto_pub -h localhost -q 0 -t 'safety/site1/event/candidate' \
  -m '{"deviceId":"SPEC-CD-02","siteId":"site1"}'
docker compose logs --since 1m hw-gateway | grep -i "candidate.*missing\|missing.*candidate"
# OK: 경고 로그 존재 + SPEC-CD-02 미등록 · NOK: 정상 처리됨
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
- **등록 게이트:** devices 테이블에 등록되고 미삭제 상태인 device에 대해서만 발행된다. 미등록 device로의 재시작 요청은 서버 단에서 400으로 거부된다.
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

**R-2. 미등록 device 거부 (OK: MQTT 발행 자체가 일어나지 않음)**
```bash
# 터미널 1 — 구독 대기 (R-1과 동일)
# 터미널 2 — heartbeat를 한 번도 보낸 적 없는 deviceId로 재시작 API 호출
# OK: API가 400 반환 AND 구독 터미널에 아무것도 수신되지 않음
# NOK: 메시지 발행됨
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
docker compose exec web-backend sqlite3 /data/sentinel.db \
  "SELECT resolved_at FROM incidents WHERE site_id='site1' ORDER BY id DESC LIMIT 1;"
# OK: resolved_at NOT NULL · NOK: NULL 유지
```

**RS-3. 서버 echo 무시 (OK: web-kind 메시지가 incident 재처리를 유발하지 않음)**
```bash
docker compose exec mosquitto mosquitto_pub -h localhost -q 1 -t 'safety/site1/alert/resolved' \
  -m '{"incidentId":0,"siteId":"site1","resolvedAt":"2026-07-02T10:31:00Z","resolvedBy":{"kind":"web","id":"admin","label":"관리자"}}'
docker compose logs --since 1m hw-gateway | grep -i "ignoring echo"
# OK: "Ignoring echo" 로그 + web-backend 호출 없음 · NOK: resolve-from-sensor 경로 진입
```

**RS-4. 알 수 없는 kind 무시 (OK: 경고 로그 + no-op)**
```bash
docker compose exec mosquitto mosquitto_pub -h localhost -q 1 -t 'safety/site1/alert/resolved' \
  -m '{"incidentId":0,"siteId":"site1","resolvedAt":"2026-07-02T10:32:00Z","resolvedBy":{"kind":"alien","id":"x","label":"x"}}'
docker compose logs --since 1m hw-gateway | grep -i "unknown resolvedBy"
# OK: 경고 로그 존재 · NOK: incident 상태 변경됨
```

**RS-5. (펌웨어) 4단계 idempotency (OK: 동일 메시지 2회 수신에도 정상)**
```bash
# RS-2의 mosquitto_pub을 연속 2회 실행 (QoS 1 중복 시뮬레이션).
# 대상 디바이스 관측:
# OK: LED/부저 OFF 유지, 에러 로그 없음, 이후 새 alert 감지/발행 가능
# NOK: GPIO 에러, 상태 머신 stuck, 감지 루틴 중복 기동
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
| web-backend 호출 실패 (incident) | 에러 로그 + 재시도 (백오프) |
| device 등록 호출 실패 | 무시 (best-effort, 재시도 없음) |
| Broker 끊김 | 자동 재연결 (지수 백오프, max 60s) |

---

## ⚠️ 리뷰 필요 (문서-코드 불일치)

기존 `docs/interfaces/mqtt-publisher-guide.md`와 실제 코드(`services/hw-gateway/main.go`)를 대조해 발견한 어긋남. 스펙 확정 전 해소 필요.

1. **`safety/{siteId}/alert`에 Sentinel도 발행한다 (테스트 알림).** 문서 토픽 표는 방향을 "H/W → Sentinel"로만 정의하지만, hw-gateway는 `POST /api/test-alert` 수신 시 같은 토픽에 QoS 2로 테스트 alert를 직접 발행한다 (`main.go` `handleTestAlert`, 토픽 `safety/%s/alert`). 방향 정의를 "H/W → Sentinel (+ Sentinel 자체 테스트 발행)"으로 갱신할지 결정 필요.

2. **테스트 alert에 `alertId`가 없다.** 문서 §3은 `alertId`를 필수(✅)로 규정하지만, `handleTestAlert`가 생성하는 `AlertPayload`에는 `alertId`가 채워지지 않는다 → 테스트 alert는 dedup 대상에서 제외된다. 필수 필드를 서버 자신이 위반하는 형태.

3. **heartbeat `status`·`alertState`는 코드상 사실상 optional.** 문서 §4는 둘 다 필수(✅)로 표기하지만, `handleHeartbeat`는 `deviceId`/`siteId`만 검증하고 `status`는 미검증, `alertState`는 누락 시 `"none"`으로 기본값 처리한다. (문서 하단 변경 이력의 "신규 필드는 optional 처리 권장"과 §4 표가 서로도 모순.)

4. **candidate의 `type`·`timestamp`를 서버가 검증하지 않는다.** 문서 §4.5는 7개 필드 전부 필수로 규정하지만, `handleCandidate`는 `deviceId`/`siteId`/`class`/`confidence`/`threshold`만 검증하고 `type`(=`voice_candidate` 고정)과 `timestamp`는 확인하지 않는다. 또한 `confidence <= 0` 거부 로직상 문서의 유효 범위 하한 `0.0`이 실제로는 거부된다.

5. **candidate만 siteId 토픽 덮어쓰기가 없다.** alert/heartbeat/alert-resolved 핸들러는 토픽의 `{siteId}`로 페이로드를 덮어쓰지만 `handleCandidate`는 페이로드 값을 그대로 사용한다. 토픽-페이로드 불일치 시 candidate만 다르게 동작.

6. **web-backend 재시도 정책 불일치.** 문서 §8은 "web-backend 호출 실패 → 1초 후 1회 재시도"라고 명시하나, 코드는 최대 **3회** 재시도 + 지수 백오프(+jitter, base 1s)를 수행한다 (`forwardToWebBackend`의 `maxRetries := 3`).

7. **timestamp 관용 처리 미문서화.** 문서 §3은 `timestamp`를 ISO 8601 필수로 규정하고 "누락 시 무시"라고 하지만, 코드(`sanitizeTimestamp`)는 (a) Unix epoch 정수 문자열도 수용하고, (b) 파싱 불가/비정상 값이면 메시지를 버리는 대신 **서버 시각으로 대체**해 incident를 생성한다. 즉 malformed timestamp는 거부되지 않는다.
