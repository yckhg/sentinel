# MQTT Publisher Guide — Sentinel

> **이 문서는 Sentinel MQTT 인터페이스의 단일 진실 원천(SSOT)입니다.**
> H/W 펌웨어 개발자(또는 다른 AI 세션)에게 이 파일 하나만 전달하면 Sentinel에 메시지를 발행할 수 있도록 작성되었습니다.
> 코드와 동기화 의무 — `services/hw-gateway/main.go` 변경 시 이 문서도 같이 수정해야 합니다 (`.claude/hooks/spec-sync-check.sh`가 자동 체크).

---

## 1. Broker 접속 정보

| 항목 | 내부 (Docker network) | 외부 (LAN/인터넷) |
|------|----------------------|-------------------|
| Host | `mosquitto` | `175.193.55.102` (공인 IP, 라우터 포트포워딩 필요) |
| Port | `1883` | `20011` |
| Scheme | `tcp://` | `tcp://` (HTTP 아님) |
| 인증 | 없음 (`allow_anonymous true`) | 없음 |
| TLS | 미사용 | 미사용 |

권장 클라이언트 옵션:
- Client ID: 디바이스마다 유일하게 (예: `esp32-press-01`)
- Clean session: `true`
- Keep-alive: `60s`

> ⚠️ 현재는 **인증/화이트리스트 없음**. 누구나 토픽에 발행할 수 있는 구조이므로 인터넷 노출 시 주의.

---

## 2. 토픽 요약

| 토픽 | 방향 | QoS | Retain | 용도 |
|------|------|-----|--------|------|
| `safety/{siteId}/alert` | H/W → Sentinel | 2 | false | 위급 상황 알림 |
| `safety/{siteId}/heartbeat` | H/W → Sentinel | 0 | false | 장비 생존 신호 |
| `safety/{siteId}/cmd/restart` | Sentinel → H/W | 1 | false | 원격 재시작 명령 |
| `safety/{siteId}/alert/resolved` | **양방향** | 1 | false | 위급 해소 통지 (sensor 버튼 또는 web operator). **🔒 spec confirmed, 서버 측 구현 예정** |

`{siteId}`는 영숫자 식별자 (예: `site1`, `factory-a`). Sentinel은 `safety/+/alert`, `safety/+/heartbeat`로 와일드카드 구독합니다.

---

## 3. `safety/{siteId}/alert` — 위급 알림

H/W가 위급 상황을 감지했을 때 발행. **반드시 QoS 2 (exactly-once).**

### Payload (JSON)

| 필드 | 타입 | 필수 | 설명 |
|------|------|------|------|
| `deviceId` | string | ✅ | 디바이스 고유 ID (예: `PRESS-01`) |
| `siteId` | string | ✅ | 사이트 ID — 토픽의 `{siteId}`와 일치해야 함 |
| `type` | string | ✅ | 알림 유형 (예: `scream`, `help`, `auto_stop`, `gas_leak`) |
| `timestamp` | string | ✅ | ISO 8601 UTC (예: `2026-04-13T09:15:30Z`) |
| `description` | string | optional | 사람이 읽을 설명 |
| `severity` | string | optional | `critical` \| `warning` |
| `test` | bool | optional | `true`이면 테스트 알림으로 표시 (실제 인시던트로 기록되되 로그/UI에 TEST 표식) |

> **누락 시 동작:** 4개 필수 필드 중 하나라도 비어 있으면 hw-gateway가 메시지를 무시하고 경고 로그만 남깁니다. 알림은 발송되지 않습니다.

### 예시

```json
{
  "deviceId": "PRESS-01",
  "siteId": "site1",
  "type": "scream",
  "description": "Scream detected near press machine #1",
  "severity": "critical",
  "timestamp": "2026-04-13T09:15:30Z"
}
```

### Sentinel 측 처리

1. JSON 파싱 + 필수 필드 검증
2. 토픽의 `{siteId}`로 페이로드의 `siteId` 덮어쓰기 (일관성)
3. **notifier**에 `POST /api/notify` (전체 페이로드 전달) — 알림 채널 발송
4. **web-backend**에 `POST /api/incidents` — DB 기록 + WebSocket 푸시
   - 전달 페이로드: `{siteId, deviceId, description, occurredAt, isTest}` (alert.timestamp → occurredAt)
5. 두 HTTP 호출은 병렬, 각 10초 타임아웃
6. **web-backend** `POST /api/devices/seen` — device 영속 등록/복원 (best-effort, 5초 타임아웃)

---

## 4. `safety/{siteId}/heartbeat` — 생존 신호

각 장비가 주기적으로 발행. **QoS 0** (한 개 빠져도 무방).

### Payload (JSON)

| 필드 | 타입 | 필수 | 설명 |
|------|------|------|------|
| `deviceId` | string | ✅ | 디바이스 고유 ID |
| `siteId` | string | ✅ | 사이트 ID |
| `timestamp` | string | optional | ISO 8601 UTC (권장 — 송신 시각 추적용) |
| `status` | string | optional | `running` \| `stopped` \| `error` |

권장 발행 주기: **10초**.

### 예시

```json
{
  "deviceId": "PRESS-01",
  "siteId": "site1",
  "status": "running",
  "timestamp": "2026-04-13T09:15:40Z"
}
```

### Sentinel 측 처리

- in-memory에 `{deviceId, siteId, alive=true, lastHeartbeat}` 갱신
- web-backend `POST /api/devices/seen` 호출 (best-effort, 5초 타임아웃) — device 영속 등록/갱신/복원
- `HEARTBEAT_TIMEOUT_SEC`(기본 30초) 동안 미수신 시 `alive=false`로 마킹 (DB의 `devices.deleted_at`은 건드리지 않음 — alive 상태와 삭제는 별개)

---

## 5. `safety/{siteId}/cmd/restart` — 재시작 명령 (구독)

Sentinel이 발행, H/W가 구독. **QoS 1.** H/W는 자기 `siteId`에 해당하는 토픽만 구독하면 됩니다 (예: `safety/site1/cmd/restart`).

### Payload (JSON)

| 필드 | 타입 | 설명 |
|------|------|------|
| `deviceId` | string | 대상 디바이스 ID |
| `siteId` | string | 사이트 ID |
| `requestedBy` | string | 명령을 내린 사용자명 |
| `reason` | string | 재시작 사유 (optional) |
| `timestamp` | string | ISO 8601 UTC |

### 예시

```json
{
  "deviceId": "PRESS-01",
  "siteId": "site1",
  "requestedBy": "admin",
  "reason": "Crisis resolved",
  "timestamp": "2026-04-13T09:30:00Z"
}
```

H/W는 페이로드의 `deviceId`가 자기 자신일 때만 재시작 동작을 수행해야 합니다 (같은 사이트 내 다른 장비가 받을 수도 있으므로).

---

## 5.5. `safety/{siteId}/alert/resolved` — 위급 해소 (양방향)

> **🔒 Spec confirmed (2026-04). 구현 예정.** 이 토픽은 펌웨어(예: sentinel-voice)가 사전에 맞춰 구현할 수 있도록 contract만 먼저 잠근 상태입니다. Sentinel 서버 측 처리(web-backend, hw-gateway 변경)는 별도 phase에서 진행됩니다.

위급 상황이 종료되었음을 시스템 전체에 알리는 토픽. **양방향 발행 + 구독** — 누가 풀든 같은 토픽이 발행되어 모든 subscriber가 동기화됩니다.

### 발행 시나리오

| 시나리오 | 발행자 | 트리거 |
|----------|--------|--------|
| **웹에서 운영자가 해제** | hw-gateway | web-backend가 incident.resolved_at 갱신 후 hw-gateway HTTP 호출 → MQTT publish |
| **현장에서 센서 물리 버튼** | 센서 펌웨어 (직접) | 디바이스의 reset/해제 버튼 입력 시 |

### 구독자 (예시)

| 구독자 | 동작 |
|--------|------|
| hw-gateway | web-backend `PATCH /api/incidents/{id}/resolve` 호출 (센서 발행분 동기화) |
| GPIO-connector | 경보등/사이렌 OFF, 자기 상태 reset, restart 명령 받을 준비 |
| 센서 자체 | 자기 LED/디스플레이에 "해제됨" 표시, 다른 운영자가 해제했음을 알림 |

### Topic 속성

| 속성 | 값 |
|------|-----|
| Direction | 양방향 (sensor ↔ Sentinel 서버) |
| QoS | 1 (at least once) |
| Retain | `false` |
| Subscribe pattern | `safety/+/alert/resolved` (와일드카드) 또는 `safety/{내siteId}/alert/resolved` |

### Payload (JSON)

| 필드 | 타입 | 필수 | 설명 |
|------|------|------|------|
| `incidentId` | number | ✅ | 해소 대상 incident ID. 모르면 `0` 허용 (서버가 가장 최근 미해결 incident 매칭 시도) |
| `siteId` | string | ✅ | 사이트 ID (토픽의 `{siteId}`와 일치) |
| `resolvedAt` | string | ✅ | ISO 8601 UTC |
| `resolvedBy` | object | ✅ | 누가 풀었는지 — 아래 구조 |
| `originalAlert` | object | optional | 원래 alert의 type/deviceId 등. subscriber가 적절히 반응하기 위함 |

#### `resolvedBy` 구조

| 필드 | 타입 | 설명 |
|------|------|------|
| `kind` | string | `"web"` \| `"sensor_button"` |
| `id` | string | web이면 사용자명(예: `"admin"`), sensor_button이면 deviceId(예: `"PRESS-01"`) |
| `label` | string | 사람이 읽는 표시명 (예: `"관리자 김현기"`, `"PRESS-01 reset 버튼"`). UI/디스플레이 표시용 |

#### `originalAlert` 구조 (optional)

| 필드 | 타입 | 설명 |
|------|------|------|
| `type` | string | 원래 alert의 type (`scream`/`gas_leak` 등) |
| `deviceId` | string | 원래 alert를 발생시킨 device |

### 예시

**웹에서 해제:**
```json
{
  "incidentId": 12345,
  "siteId": "site1",
  "resolvedAt": "2026-04-13T10:30:00Z",
  "resolvedBy": {
    "kind": "web",
    "id": "admin",
    "label": "관리자 김현기"
  },
  "originalAlert": {
    "type": "scream",
    "deviceId": "PRESS-01"
  }
}
```

**센서 물리 버튼으로 해제:**
```json
{
  "incidentId": 12345,
  "siteId": "site1",
  "resolvedAt": "2026-04-13T10:30:00Z",
  "resolvedBy": {
    "kind": "sensor_button",
    "id": "VOICE-01",
    "label": "VOICE-01 reset 버튼"
  },
  "originalAlert": {
    "type": "scream",
    "deviceId": "PRESS-01"
  }
}
```

### 펌웨어 측 구현 가이드 (sentinel-voice 등 H/W 개발자용)

1. **물리 버튼 발행 (publish):** 디바이스에 reset/해제 버튼이 있다면 누름 감지 시 위 페이로드로 `safety/{내siteId}/alert/resolved` 발행. `incidentId`를 모르면 `0`.
2. **다른 곳의 해제 알림 수신 (subscribe):** `safety/{내siteId}/alert/resolved` 구독 → 자기 LED/부저 OFF, 디스플레이에 누가 풀었는지 표시.
3. **idempotency:** 같은 incident에 대해 중복 resolved 메시지가 와도 안전해야 합니다. 자기 자신이 발행한 메시지가 broker echo로 다시 들어와도 무해.
4. **자기 자신 반응 차단 옵션:** 발행자 `resolvedBy.id == 내 deviceId`면 무시 (선택). 보통은 처리해도 무해.

### 정책 메모

- **사람 게이트 원칙:** alert는 자동 해제되지 않습니다. 반드시 사람(운영자 클릭 또는 현장 버튼 누름)이 트리거해야 합니다.
- **Auto-restart 분리:** 본 토픽은 "위기 종료 통지"이지 "장비 재가동 명령"이 아닙니다. 장비 재가동은 별도 `safety/{siteId}/cmd/restart` 명령이 필요합니다.
- **다중 incident:** 같은 site에 동시에 여러 incident가 있을 수 있습니다. `incidentId`로 개별 매칭. `0`이면 가장 최근 미해결.

---

## 6. Device ID 정책 / 자동 등록

- **자동 등록 (구현됨, 2026-04):** hw-gateway는 `safety/+/heartbeat` 또는 `safety/+/alert` 수신 시 web-backend `POST /api/devices/seen`을 호출하여 처음 보는 `(siteId, deviceId)` 조합을 `devices` 테이블에 자동 등록합니다. 기존 row면 `last_seen`만 갱신. 사전 프로비저닝 불필요.
- **영속 (Manual-only delete):** 한 번 등록된 device는 운영자가 명시적으로 삭제(soft delete)하지 않는 한 영속됩니다. 컨테이너 재시작·hw-gateway 메모리 초기화와 무관.
- **복원:** soft-deleted(`deleted_at != NULL`) device가 다시 heartbeat/alert를 보내면 `deleted_at`이 자동으로 `NULL`로 초기화되어 복원됩니다.
- **alert 기록:** alert 수신 시 web-backend `POST /api/incidents`에도 `deviceId`가 함께 전송되어 `incidents.device_id` 컬럼에 저장됩니다 (사고 추적용).
- **Restart 검증:** web-backend `POST /api/equipment/restart`는 `devices` 테이블에 등록되고 미삭제 상태인 device에 대해서만 허용됩니다. 미등록이면 `400`. 최초 한 번 heartbeat가 들어오기 전까지는 restart 불가.
- 운영자는 web UI에서 device에 **alias(별칭)**를 부여할 수 있습니다.
- `deviceId`는 펌웨어에 하드코딩하거나 디바이스 시리얼/MAC 기반으로 안정적으로 생성하세요. 재부팅마다 바뀌면 새 row로 등록됩니다.
- `siteId`도 동일 — 펌웨어 빌드 시 사이트별로 굽거나 설정 파일로 주입.

> `/api/devices/seen` 호출은 best-effort (5초 타임아웃, 재시도 없음). 실패해도 alert/heartbeat 처리 자체는 계속됩니다. 단, alert 경로에서는 `POST /api/incidents` 자체가 devices UPSERT를 보장합니다.

---

## 7. 자가 점검 — 내 메시지가 처리됐는지 확인

### 7.1 발행 직후 hw-gateway 로그 확인

```bash
docker compose logs -f hw-gateway
```

성공 시 다음과 같은 라인이 보입니다:
```
[MQTT] Received alert on topic: safety/site1/alert
[ALERT] deviceId=PRESS-01 siteId=site1 type=scream severity=critical
```

실패 시 (필수 필드 누락 등):
```
[MQTT] Missing required fields in alert payload: {...}
```

### 7.2 mosquitto에 직접 echo 테스트

발행 측에서:
```bash
mosquitto_pub -h <host> -p <port> -q 2 -t 'safety/site1/alert' \
  -m '{"deviceId":"TEST-01","siteId":"site1","type":"scream","timestamp":"2026-04-13T10:00:00Z"}'
```

수신 측에서 (Sentinel 호스트):
```bash
docker compose exec mosquitto mosquitto_sub -v -t 'safety/#'
```

### 7.3 web-backend incident 기록 확인

```bash
docker compose exec web-backend sqlite3 /data/sentinel.db \
  "SELECT id, site_id, description, occurred_at FROM incidents ORDER BY id DESC LIMIT 5;"
```

---

## 8. QoS / 에러 정책 요약

| 시나리오 | 동작 |
|----------|------|
| Malformed JSON | 무시 + 에러 로그 |
| 필수 필드 누락 | 무시 + 경고 로그 |
| notifier 호출 실패 | 에러 로그, web-backend 호출은 계속 |
| web-backend 호출 실패 | 에러 로그, 1초 후 1회 재시도 |
| Broker 끊김 | 자동 재연결 (지수 백오프, max 60s) |

---

## 9. Sentinel 측 클라이언트 설정 (참고용)

| 항목 | 값 |
|------|-----|
| Client ID | `sentinel-hw-gateway` |
| Clean session | `true` |
| Keep-alive | 60초 |
| Auto-reconnect | 활성 (1s → 2s → … → 60s 지수 백오프) |
| Broker URL env | `MQTT_BROKER_URL` (기본 `tcp://mosquitto:1883`) |

---

## 변경 이력 관리

이 문서가 SSOT입니다. 다음 코드 영역과 짝을 이룹니다:

- `services/hw-gateway/main.go` — `AlertPayload`, `HeartbeatPayload`, `RestartMQTTPayload`, `subscribeTopics()`, `handleAlert()`, `handleHeartbeat()`
- `services/hw-gateway/AGENTS.md` — 요약만 유지, 상세는 이 문서로 링크

스펙 변경 시 위 코드와 본 문서를 같은 커밋에 함께 수정하세요. `.claude/hooks/mqtt-spec-sync-check.sh`가 한쪽만 변경되면 경고합니다.
