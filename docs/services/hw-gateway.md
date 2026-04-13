# hw-gateway

> **Reader scope:** 이 서비스를 구현·수정하는 Claude 세션 전용.
> 다른 서비스의 내부 구현을 읽지 마세요. 외부와의 계약은 아래 "Interfaces" 섹션의 링크만 참조.
> 시스템 전체 그림은 orchestrator 세션 영역(`docs/architecture-overview.md`)이며 본 세션은 읽을 필요 없음.

## Responsibility

H/W(MQTT)와 S/W(HTTP) 사이의 유일한 접점. MQTT에서 받은 crisis/heartbeat 신호를 HTTP 호출로 변환하고, 반대로 S/W에서 온 restart 명령을 MQTT로 발행한다. 장비 alive/dead 상태를 in-memory로 관리한다.

## Interfaces

| Boundary | Direction | Spec |
|----------|-----------|------|
| MQTT broker (`mosquitto:1883`) | subscribe + publish | [../interfaces/mqtt-publisher-guide.md](../interfaces/mqtt-publisher-guide.md) |
| Inbound HTTP (web-backend → 본 서비스) | inbound | 본 문서 "HTTP API" |
| Outbound HTTP (notifier `POST /api/notify`) | outbound | 본 문서 "Outbound Calls" |
| Outbound HTTP (web-backend `POST /api/incidents`) | outbound | 본 문서 "Outbound Calls" |

## Code Structure

단일 파일: `services/hw-gateway/main.go` (~550 lines).
- MQTT 클라이언트 초기화 + `safety/+/alert`, `safety/+/heartbeat` 구독
- crisis 수신 → notifier와 web-backend로 병렬 forward (goroutine + context timeout)
- heartbeat → in-memory map 갱신
- HTTP 서버 포트 8080 (healthz/equipment/restart)
- 주기 ticker가 `HEARTBEAT_TIMEOUT_SEC` 경과 장비를 dead로 마킹

## Environment Variables

| Var | Default | Meaning |
|-----|---------|---------|
| `MQTT_BROKER_URL` | `tcp://mosquitto:1883` | MQTT 브로커 |
| `NOTIFIER_URL` | `http://notifier:8080` | crisis forward 대상 |
| `WEB_BACKEND_URL` | `http://web-backend:8080` | incident 기록 대상 |
| `HEARTBEAT_TIMEOUT_SEC` | `30` | 이 시간 이상 heartbeat 없으면 dead |

## Build & Run

```bash
docker compose build hw-gateway
docker compose up -d hw-gateway
docker compose logs -f hw-gateway
```

- 포트: 내부 8080만 (외부 노출 없음)
- 헬스: `GET /healthz` (compose healthcheck에서 사용)
- 단독 동작 확인: `docker exec sentinel-mosquitto mosquitto_pub -t safety/site-1/heartbeat -m '{...}'` 후 `curl http://sentinel-hw-gateway:8080/api/equipment/status` (다른 컨테이너에서)

## HTTP API

| Method | Path | Request | Response | Purpose |
|--------|------|---------|----------|---------|
| GET | `/healthz` | — | `200 OK` | 헬스체크 |
| GET | `/api/equipment/status` | — | `[{deviceId, siteId, alive, lastHeartbeat}]` | in-memory 장비 상태 |
| POST | `/api/restart` | `{deviceId}` | `202 Accepted` | restart 명령 수신 → MQTT publish |

## Outbound Calls

1. **notifier** `POST http://notifier:8080/api/notify`
   - 페이로드: crisis 이벤트 (siteId, deviceId, timestamp, description)
   - 타임아웃 10s, 실패 시 exponential backoff + jitter 재시도
2. **web-backend** `POST http://web-backend:8080/api/incidents`
   - 페이로드: 같은 crisis 이벤트 (incident 기록 + WebSocket push 트리거)
   - notifier 호출과 **병렬** 실행 (goroutine)

받는 쪽 내부 구현은 알 필요 없음. URL과 페이로드 형태만 맞추면 된다.

## Constraints / Known Issues

- MQTT 자동 재연결 (exponential backoff). 브로커 다운 시 대기 중인 메시지는 로컬 버퍼 없음 → 유실 가능.
- crisis forward는 at-most-once가 아니라 재시도 기반. 수신 측(notifier, web-backend)은 멱등 처리가 유리하나 현재는 단순 중복 발생 가능.
- 장비 상태는 휘발성. 재시작 시 모두 "unknown" → 첫 heartbeat까지 dead.
- MQTT 스펙 변경 시 `.claude/hooks/mqtt-spec-sync-check.sh`가 `interfaces/mqtt-publisher-guide.md`와 `main.go` 동시 수정을 강제한다.

## Storage / State

- **In-memory only.** `map[deviceId]EquipmentStatus` + `sync.RWMutex`. DB 없음.
