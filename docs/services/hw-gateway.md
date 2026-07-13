# hw-gateway

> **Reader scope:** 이 서비스를 구현·수정하는 Claude 세션 전용.
> 다른 서비스의 내부 구현을 읽지 마세요. 외부와의 계약은 아래 "Interfaces" 섹션의 링크만 참조.
> 시스템 전체 그림은 orchestrator 세션 영역(`docs/architecture-overview.md`)이며 본 세션은 읽을 필요 없음.

> **⚠ 계약 정본(SSOT) = [`docs/spec/hw-gateway.md`](../spec/hw-gateway.md).**
> 이 문서는 구현 세션용 **항해 안내**일 뿐이며, 계약(입출력·응답 코드·env 의미·신뢰성·검증 단언)은 정본 스펙이 소유한다. 본 문서와 정본이 어긋나면 **정본이 우선**한다. 계약을 바꾸려면 이 문서가 아니라 정본 스펙을 고쳐야 한다.
> 접면(경계) 스키마의 소유자: MQTT 토픽·페이로드는 [`docs/spec/interface-mqtt.md`](../spec/interface-mqtt.md), HTTP 요청/응답은 [`docs/spec/interface-web-api.md`](../spec/interface-web-api.md).

## Responsibility

H/W(MQTT)와 S/W(HTTP) 사이의 유일한 접점. MQTT에서 받은 alert/heartbeat/candidate/resolved 신호를 HTTP 호출로 변환하고, 반대로 S/W에서 온 restart·웹 해소·테스트 알림 명령을 MQTT로 발행한다. 장비 alive/dead 상태와 경보 상태를 in-memory로 관리한다. **상태 비저장(휘발성)** — DB 없음, 영속 등록은 web-backend에 위임(best-effort).

정확한 의도·범위는 정본 스펙 §목적/의도 참조.

## Interfaces

| Boundary | Direction | Contract (SSOT) |
|----------|-----------|-----------------|
| MQTT broker (`mosquitto:1883`) | subscribe + publish | [`docs/spec/interface-mqtt.md`](../spec/interface-mqtt.md) (토픽·QoS·retain·페이로드) |
| Inbound HTTP (web-backend → 본 서비스) | inbound | [`docs/spec/interface-web-api.md`](../spec/interface-web-api.md) 계약 15 |
| Outbound HTTP (notifier `POST /api/notify`) | outbound | [`docs/spec/notifier.md`](../spec/notifier.md) + 정본 §출력 |
| Outbound HTTP (web-backend `POST /api/incidents` · `/api/devices/seen` · `/api/incidents/{id}/resolve-from-sensor`) | outbound | [`docs/spec/interface-web-api.md`](../spec/interface-web-api.md) 계약 13 + 정본 §출력 |

접면 스키마와 신뢰성 계약(타임아웃·재시도 유무·성공 판정)은 위 SSOT가 소유한다. 구현 시 재현하지 말고 정본을 읽어라.

## Code Structure

단일 파일: `services/hw-gateway/main.go`.
- MQTT 클라이언트 초기화 + **4개 토픽 구독**: `safety/+/alert`, `safety/+/heartbeat`, `safety/+/alert/resolved`, `safety/+/event/candidate`. (persistent session / clean session=false / 고정 client ID `sentinel-hw-gateway` — 정본 §의존 도구·§회복력 참조.)
- **retained-message 방어:** 모든 구독 핸들러 진입 즉시 `msg.Retained()`를 검사한다. 계약이 전 `safety/#` 토픽 retain=false를 보장하므로(`docs/spec/interface-mqtt.md`), retained 플래그가 켜진 메시지는 stale/계약 위반으로 간주해 **처리하지 않고 드롭 + `[RETAINED]` 경고 로그**만 남긴다 (`isRetainedMessage` 공용 헬퍼). non-retained 메시지 흐름은 이전과 완전히 동일.
- alert 수신 → notifier와 web-backend로 병렬 forward (goroutine + context timeout). `alertId` 기반 in-memory dedup (등록은 web-backend forward 2xx 성공 이후).
- heartbeat → in-memory map 갱신 (`siteId:deviceId` 복합 키).
- candidate 수신 → 로그 + `POST /api/devices/seen`만 (incident/notifier 없음).
- alert/resolved 수신 → `resolvedBy.kind` 분기: `"web"`은 자기 echo로 무시, `"sensor_button"`은 web-backend `POST /api/incidents/{id}/resolve-from-sensor`로 forward.
- HTTP 서버 포트 8080 (healthz / equipment/status / restart / alert/resolved / test-alert).
- 주기 ticker: `HEARTBEAT_TIMEOUT_SEC` 경과 장비 dead 마킹, 장비 스토어 상한(`EQUIPMENT_MAX_DEVICES`) LRU eviction + `EQUIPMENT_EVICT_TTL_SEC` TTL 청소.

동작(입력 처리 규칙·응답 코드·불변식)의 정본은 스펙 §핵심 로직·§검증 단언이다.

## Environment Variables

env 변수 목록·기본값·의미·기동 불변식(`EQUIPMENT_EVICT_TTL_SEC > HEARTBEAT_TIMEOUT_SEC`)의 정본은 [`docs/spec/hw-gateway.md`](../spec/hw-gateway.md) §의존 도구·시스템 (환경 변수 계약 표)이다. 여기서 재정의하지 않는다.

## Build & Run

```bash
docker compose build hw-gateway
docker compose up -d hw-gateway
docker compose logs -f hw-gateway
```

- 포트: 내부 8080만 (외부 노출 없음)
- 헬스: `GET /healthz` (compose healthcheck에서 사용 — MQTT 연결·경보성 필수 3토픽 구독 성립을 반영하는 200/503 계약. 정본 §헬스체크(healthz) 계약 참조)
- 단독 동작 확인: `docker exec sentinel-mosquitto mosquitto_pub -t safety/site-1/heartbeat -m '{...}'` 후 `curl http://sentinel-hw-gateway:8080/api/equipment/status` (다른 컨테이너에서)

## HTTP API

HTTP 엔드포인트의 요청/응답 스키마·상태 코드는 정본 계약이 소유한다 — 여기서 재정의하지 않는다.

- 엔드포인트 목록·의미: [`docs/spec/hw-gateway.md`](../spec/hw-gateway.md) §입력(Inbound HTTP) / §출력.
- 요청/응답 스키마: [`docs/spec/interface-web-api.md`](../spec/interface-web-api.md) 계약 15.

핵심 계약 요지(정본 우선, 여기 값이 어긋나면 정본을 따르라):
- `GET /healthz` — 200(`"status":"ok"`) / 503(`"status":"degraded"`, MQTT 미연결·미구독). hang 없이 ≤1s.
- `GET /api/equipment/status` — `[{deviceId, siteId, alive, lastHeartbeat, alertState}]`, 브로커 상태 무관 200.
- `POST /api/restart` — `siteId`·`deviceId` 모두 필수(누락 400), 성공 `200` `{"status":"sent","topic":...}`.
- `POST /api/alert/resolved` — 웹 발 해소 → MQTT 발행 (`resolvedBy.kind=="web"` echo는 무시).
- `POST /api/test-alert` — 테스트 alert → 실제 alert 경로로 순환 유입(`isTest` 전파).
- 발행 3종(restart / alert/resolved / test-alert)의 브로커 상태별 응답(미연결 503 / 단절 시 발행 타임아웃 5초 → 503 / 발행실패 500)은 정본 §MQTT 발행 API 공통 응답 계약.

## Outbound Calls

호출 대상·트리거·신뢰성 계약(타임아웃·재시도 유무·성공 판정)의 정본은 [`docs/spec/hw-gateway.md`](../spec/hw-gateway.md) §출력(Outbound HTTP)이다.

요지(정본 우선):
1. **notifier** `POST /api/notify` — alert 수신 시. 타임아웃 10s, **재시도 없음**, 실패는 로그만.
2. **web-backend** `POST /api/incidents` — alert 수신 시(notifier와 병렬). 타임아웃 10s, **transport 에러 및 HTTP 5xx에 한해 최대 3회** 지수 백오프+jitter 재시도, **4xx는 미재시도**, 2xx에서만 성공. `deviceId` + `alertId`(DB dedup 키) 포함.
3. **web-backend** `POST /api/devices/seen` — heartbeat/alert/candidate 수신 시마다. best-effort, 타임아웃 5s, 재시도 없음.
4. **web-backend** `POST /api/incidents/{id}/resolve-from-sensor` — `alert/resolved` + `resolvedBy.kind=="sensor_button"`. 타임아웃 5s, 재시도 없음, `incidentId==0`이면 수신 측이 최근 미해결 incident 매칭.

받는 쪽 내부 구현은 알 필요 없음. URL과 페이로드 형태만 맞추면 된다.

## Constraints / Known Issues

- MQTT 자동 재연결 (지수 백오프 최대 60s). 브로커 다운 중 하드웨어 발행 메시지에 대한 **로컬 버퍼는 없다**. 단 gateway는 persistent session(clean=false)+고정 client ID로 접속하므로, gateway만 끊겼다 재접속하는 경계에서 브로커 세션 큐가 QoS1/2 메시지를 재전송해 **수신측 경보 유실을 방지**한다(브로커 세션 지속성 설정에 의존). 정본 §회복력·단언 S 참조.
- alert forward는 at-most-once가 아니라 재시도 기반. web-backend는 `alertId`로 DB dedup하고, 본 서비스도 `alertId` 단위 in-memory dedup을 둔다. in-memory dedup 등록은 web-backend forward가 2xx로 성공한 **이후에만** 이뤄지므로, forward가 최종 실패(5xx 재시도 소진 등)한 이벤트는 dedup에 막히지 않고 펌웨어 재전송으로 복구될 수 있다.
- 장비 상태는 휘발성. 재시작 직후 `GET /api/equipment/status`는 빈 배열 `[]`이며, 각 장비의 첫 heartbeat까지 그 장비는 목록에 없다.
- MQTT 스펙 변경 시 `.claude/hooks/mqtt-spec-sync-check.sh`가 `interfaces/mqtt-publisher-guide.md`와 `main.go` 동시 수정을 강제한다.
- **Retained-message 자동 해소 방어:** 브로커에 retained resolve 메시지가 잔존하면 hw-gateway 재시작/재구독 시마다 재전달되어 최근 미해결 incident를 사람 개입 없이 자동 해소할 위험이 있다(사람 게이트 위반). 이를 위해 hw-gateway는 retained 메시지를 드롭한다(위 "retained-message 방어" 참조). **근본 원인은 발행자 측 retain=1 발행**이며(sentinel-voice 펌웨어에서 수정), 이미 브로커에 잔존 중인 retained 메시지는 운영자가 `mosquitto_pub -r -n -t safety/site1/alert/resolved`로 별도 clear해야 한다 (브로커 상태 변경 = 승인 필요 작업).

## Storage / State

- **In-memory only.** `siteId:deviceId` 복합 키 → EquipmentStatus 맵 + `sync.RWMutex`. DB 없음. 스토어는 보존 상한(LRU) + TTL로 유한하게 유지된다(정본 §장비 스토어 보존 상한·eviction).
