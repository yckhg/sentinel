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
| `mosquitto` | 내부 only | 현재 20011로 임시 노출 중 — 운영 진입 전 제거 의무 ([운영 정책 C](#정책-c--mosquitto-20011-외부-노출은-테스트-한정)) |
| `hw-gateway` / `notifier` | 내부 only | — |

## 컴포넌트 책임

| 서비스 | 책임 | SSOT 문서 |
|--------|------|-----------|
| `mosquitto` | MQTT broker. 센서/액추에이터 메시지 라우팅 | — |
| `hw-gateway` | MQTT ↔ HTTP 변환 (S/W 측 유일 MQTT 클라이언트) | [interfaces/mqtt-publisher-guide.md](./interfaces/mqtt-publisher-guide.md) |
| `cctv-adapter` | RTSP → RTMP 변환 push | [interfaces/streaming-api.md](./interfaces/streaming-api.md) |
| `youtube-adapter` | YouTube 라이브/로컬 파일 → RTMP push (데모/테스트용) | [interfaces/streaming-api.md](./interfaces/streaming-api.md) |
| `streaming` | RTMP 수신, HLS 서빙. 스트림 상태 SSOT | [interfaces/streaming-api.md](./interfaces/streaming-api.md) |
| `recording` | streaming RTMP 풀 → FFmpeg 녹화 (롤링 윈도우 + 아카이브) | — |
| `notifier` | KakaoTalk/SMS 발송. fallback 체인 | — |
| `web-backend` | REST + WebSocket. SQLite 영속화. 임시 링크 발급 | [interfaces/web-api.md](./interfaces/web-api.md) |
| `web-frontend` | 모바일 우선 UI | [interfaces/web-api.md](./interfaces/web-api.md) (소비자) |

외부 H/W 컴포넌트:
- **센서 (음성 위험 감지)** — MQTT publish only. 스펙: [mqtt-publisher-guide.md](./mqtt-publisher-guide.md)
- **GPIO-connector (예정)** — MQTT subscribe → 정해진 전류 출력으로 장비 정지. 스펙은 위와 동일 (subscriber 측)
- **CCTV** — RTMP/RTSP 송신. 어떤 제품이든 우리 RTMP 규격 준수 시 교체 자유

비-운영 컴포넌트(테스트 한정): `e2e-crisis`, `e2e-restart` — docker-compose의 별도 profile로 격리.

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

## 운영 정책

본 시스템의 운영 가정과 보안 전제. 변경 시 코드/배포/문서 동시 검토 필요.

### 정책 A — Single-tenant, per-site deployment

- **1 작업장 = 1 mini PC = 1 Sentinel 인스턴스.** 중앙 관리 플레인 없음.
- 멀티테넌트/멀티사이트 기능은 로드맵에 없다. 추가 작업장은 인스턴스를 추가 배포한다.
- 센서/장비 관리(등록·별칭·soft delete)는 각 인스턴스가 독립적으로 수행한다. 중앙 device registry 없음.
- `siteId`는 펌웨어가 박아 보내는 식별자다. 본 SW는 저장/표시만 수행하며 발급/관리 주체가 아니다.
- 코드/UI는 단일 사이트 가정을 깨지 않도록 작성한다. 멀티사이트 분기 도입 금지.
- 향후 "여러 인스턴스를 한 화면에 모으는 중앙 대시보드"가 필요해지면 별도 서비스로 분리한다 (본 인스턴스는 변경 없음).

### 정책 B — MQTT 인증 미적용 (현 단계)

- 현재 mosquitto는 `allow_anonymous true`로 무인증 운영한다.
- **근거:** 모든 publisher/subscriber가 같은 사설 LAN(`yc-network`)에 격리. 네트워크 레벨 격리가 1차 방어선이며, 본 단계의 위협 모델 안에서 충분하다.
- **전제 조건 (엄수):** 운영 진입 시 mosquitto 20011 외부 포트 노출 제거 (정책 C 참조). 이 전제가 깨지면 본 정책의 근거가 무효화된다.
- **재평가 트리거 (아래 중 하나라도 발생하면 인증 도입 검토):**
  - 외부 원격 유지보수 필요 (VPN 아닌 직접 경로)
  - 산업안전 인증(KOSHA 등) 요구
  - 내부 LAN에 비신뢰 장비(BYOD, 외부 벤더 장비) 유입
  - 인터넷 경유 MQTT 필요

### 정책 C — mosquitto 20011 외부 노출은 테스트 한정

- 현재 `docker-compose.yml`의 `mosquitto.ports: 20011:1883`는 펌웨어 개발 환경 제약(개발 PC가 mini PC와 다른 LAN에 있을 수 있음)으로 인한 임시 노출이다.
- **운영 진입 전 제거 의무.** 제거 이후 모든 펌웨어는 mini PC와 같은 LAN에 배치되어 mosquitto에 직접 접속한다.
- 이 정책은 정책 B의 근거를 유지하기 위한 필수 조건이다.

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

## 문서 구조 (orchestrator 관리 영역)

- 본 문서 — 시스템 전체 그림. **orchestrator 세션 전용** (구현 세션은 읽을 필요 없음)
- `interfaces/` — 서비스 간 계약 SSOT. 해당 boundary에 닿는 세션만 읽음
- `services/<name>.md` — 서비스별 구현 가이드. 해당 서비스를 작업하는 세션만 읽음

신규 구현 세션 위임 시 `services/<name>.md` + 닿는 `interfaces/*.md`만 지정. 본 문서는 전달 금지 (컨텍스트 비대화).

## 변경 시 주의

- 외부 노출 정책이 바뀌면 이 문서를 먼저 갱신
- 새 컴포넌트 추가 시 토폴로지 다이어그램 + 책임 표 갱신
- 운영 진입 시 mosquitto 20011 노출 제거 + 인증 도입 검토 필요 (→ [운영 정책 B](#정책-b--mqtt-인증-미적용-현-단계), [C](#정책-c--mosquitto-20011-외부-노출은-테스트-한정))
- 멀티사이트/중앙 대시보드 요구 발생 시 본 인스턴스 변경 금지, 별도 서비스로 분리 (→ [운영 정책 A](#정책-a--single-tenant-per-site-deployment))
