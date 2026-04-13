# MQTT Publisher Guide — Sentinel

> **이 문서는 Sentinel MQTT 인터페이스의 단일 진실 원천(SSOT)입니다.**
> H/W 펌웨어 개발자(또는 다른 AI 세션)에게 이 파일 하나만 전달하면 Sentinel에 메시지를 발행할 수 있도록 작성되었습니다.
> 코드와 동기화 의무 — `services/hw-gateway/main.go` 변경 시 이 문서도 같이 수정해야 합니다 (`.claude/hooks/mqtt-spec-sync-check.sh`가 자동 체크).

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
   - 전달 페이로드: `{siteId, description, occurredAt, isTest}` (alert.timestamp → occurredAt)
5. 두 HTTP 호출은 병렬, 각 10초 타임아웃

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
- DB 저장 없음, HTTP 포워딩 없음
- `HEARTBEAT_TIMEOUT_SEC`(기본 30초) 동안 미수신 시 `alive=false`로 마킹

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

## 6. Device ID 정책

- **자동 수집 모델 (예정):** Sentinel은 처음 보는 `deviceId`를 자동으로 등록합니다. 사전 프로비저닝 불필요.
- 운영자는 web UI에서 자동 등록된 디바이스에 **이름/별칭**만 부여합니다.
- `deviceId`는 펌웨어에 하드코딩하거나 디바이스 시리얼/MAC 기반으로 안정적으로 생성하세요. 재부팅마다 바뀌면 안 됩니다.
- `siteId`도 동일 — 펌웨어 빌드 시 사이트별로 굽거나 설정 파일로 주입.

> 자동 수집 기능은 현재 미구현 (2026-04 기준). 현재는 hw-gateway 메모리에만 추적되고 incidents 테이블에는 deviceId가 저장되지 않습니다.

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
