# Architecture Overview — Sentinel

산업안전 실시간 모니터링 시스템. 단일 mini PC 온프레미스 운영, 폐쇄망 기반.

## 토폴로지

```
┌─ 내부 LAN (사설망) ────────────────────────────────────────┐
│                                                            │
│   [CCTV] ──RTMP/RTSP──> [streaming] ─HLS─┐                 │
│                                          │                 │
│   [센서(음성)] ──pub──┐                  │                 │
│                       ▼                  │                 │
│                  [mosquitto]             │                 │
│                       │  ▲               │                 │
│              ┌────────┘  └────────┐      │                 │
│              ▼                    ▼      ▼                 │
│         [hw-gateway] ──HTTP──> [web-backend] ──┐           │
│              ▲                                 │           │
│   [GPIO-connector(예정)]──sub─┘                │           │
│                                                │           │
└────────────────────────────────────────────────│───────────┘
                                                 ▼
                                         [web-frontend]
                                              │ (외부 공개 80/443)
                                              ▼
                                       공유 링크 (외부 시청)
```

## 외부 노출 정책

| 컴포넌트 | 노출 | 비고 |
|----------|------|------|
| `web-frontend` / `web-backend` | **공개** | 유일한 외부 접점. 인증 + 임시 링크 발급 책임 |
| `streaming` | 내부 only | web-backend가 HLS URL만 외부에 중계 |
| `mosquitto` | 내부 only | 단, 현재 20011로 임시 노출 중 (테스트 환경 한계, 운영 진입 시 제거) |
| `hw-gateway` / `notifier` | 내부 only | — |

## 컴포넌트 책임

| 서비스 | 책임 | SSOT 문서 |
|--------|------|-----------|
| `mosquitto` | MQTT broker. 센서/액추에이터 메시지 라우팅 | — |
| `hw-gateway` | MQTT ↔ HTTP 변환 (S/W 측 유일 MQTT 클라이언트) | [mqtt-publisher-guide.md](./mqtt-publisher-guide.md) |
| `cctv-adapter` | RTSP → RTMP 변환 push | [streaming-api.md](./streaming-api.md) |
| `streaming` | RTMP 수신, HLS 서빙. 스트림 상태 SSOT | [streaming-api.md](./streaming-api.md) |
| `notifier` | KakaoTalk/SMS 발송. fallback 체인 | — |
| `web-backend` | REST + WebSocket. SQLite 영속화. 임시 링크 발급 | — |
| `web-frontend` | 모바일 우선 UI | — |

외부 H/W 컴포넌트:
- **센서 (음성 위험 감지)** — MQTT publish only. 스펙: [mqtt-publisher-guide.md](./mqtt-publisher-guide.md)
- **GPIO-connector (예정)** — MQTT subscribe → 정해진 전류 출력으로 장비 정지. 스펙은 위와 동일 (subscriber 측)
- **CCTV** — RTMP/RTSP 송신. 어떤 제품이든 우리 RTMP 규격 준수 시 교체 자유

## 위기 대응 흐름

```
센서 ──[safety/{siteId}/alert]──> mosquitto ──┬── hw-gateway ──┬── notifier ── KakaoTalk/SMS
                                              │                └── web-backend ── WebSocket → 사용자
                                              └── GPIO-connector(예정) ── 장비 정지
```

원격 사용자는 알림에 포함된 임시 링크로 web-frontend 접속 → CCTV HLS 실시간 확인 → 필요 시 장비 재시작 명령.

## 설계 원칙 (요약)

1. **H/W-S/W 계층 분리** — S/W는 신호만 받음
2. **단일 H/W 접점** — hw-gateway 하나로 통일
3. **벤더 독립성** — CCTV/센서/액추에이터 모두 우리 규격(RTMP, MQTT) 준수 시 교체 자유
4. **streaming = 스트림 상태 SSOT** — 다른 서비스는 status 보고하지 않음
5. **상대 URL** — 클라이언트에 반환되는 모든 URL은 상대 경로 (Docker 내부 주소 노출 금지)
6. **모바일 우선 / 경량 우선** — mini PC 단일 호스트에서 동작

## 기술 스택

| 영역 | 선택 |
|------|------|
| Containerization | Docker Compose (단일 호스트) |
| MQTT broker | eclipse-mosquitto:2 |
| Streaming | nginx-rtmp + HLS (no transcoding) |
| Backend | Go + SQLite |
| Frontend | 모바일 우선 web |
| 인증 (MQTT) | 없음 (내부망 가정) |
| 인증 (Web) | web-backend 책임 |

## 변경 시 주의

- 외부 노출 정책이 바뀌면 이 문서를 먼저 갱신
- 새 컴포넌트 추가 시 토폴로지 다이어그램 + 책임 표 갱신
- 운영 진입 시 mosquitto 20011 노출 제거 + 인증 도입 검토 필요
