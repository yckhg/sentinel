# Sentinel 문서 인덱스

> 역할별 진입점 맵. 어떤 세션이든 이 파일부터 읽고 필요한 최소 문서(3~5개)만 선택해 로드하세요.
> 전체 문서를 다 읽지 마세요. Sentinel은 **MSA-스타일 컨텍스트 격리** 원칙을 따릅니다.

## 1. 디렉토리 트리

```
docs/
├── README.md                          ← 본 파일 (역할별 진입점)
├── architecture-overview.md           ← 시스템 전체 그림 (orchestrator 전용)
├── adapter-checklist.md               ← 새 adapter/video source 추가 절차
├── operational-rules.md               ← 네이밍/설정/모니터링 정책
├── interfaces/                        ← 서비스 간 계약 SSOT
│   ├── mqtt-publisher-guide.md        ← MQTT 토픽/페이로드 SSOT (펌웨어 진입점)
│   ├── streaming-api.md               ← CCTV/streaming 어댑터 통합 SSOT
│   └── web-api.md                     ← web-backend HTTP/WebSocket SSOT
└── services/                          ← 서비스별 구현 가이드
    ├── hw-gateway.md                  ← MQTT ↔ DB ↔ notifier 중계
    ├── cctv-adapter.md                ← RTSP → HLS 변환 어댑터
    ├── youtube-adapter.md             ← YouTube Live → HLS 어댑터
    ├── streaming.md                   ← HLS 세그먼트 배포
    ├── recording.md                   ← 세그먼트 저장/보존
    ├── notifier.md                    ← 알림 라우팅 (SMS/Kakao/Web)
    ├── web-backend.md                 ← HTTP API + WebSocket 허브
    └── web-frontend.md                ← 모바일 우선 React UI
```

## 2. 독자별 진입점 맵

각 독자는 아래 나열된 파일만 읽으세요. 다른 영역은 읽지 마세요.

### (a) 펌웨어 개발자 (sentinel-voice 등 H/W 측)

단일 진입점입니다. 이 파일 하나로 MQTT 발행/수신 모두 구현 가능합니다.

1. `interfaces/mqtt-publisher-guide.md` — **필수, 단일 진입점**

그 외 문서는 읽을 필요 없음. 서버 내부 구현(services/)은 펌웨어와 무관.

### (b) 서버 개발자 — 서비스별

한 번에 하나의 서비스만 담당하세요. 본인 서비스 doc + 해당 서비스가 사용하는 interface doc만 읽습니다.

| 서비스 | 반드시 읽을 파일 (3~5개) |
|---|---|
| **hw-gateway** | `services/hw-gateway.md`, `interfaces/mqtt-publisher-guide.md` |
| **cctv-adapter** | `services/cctv-adapter.md`, `interfaces/streaming-api.md`, `adapter-checklist.md` |
| **youtube-adapter** | `services/youtube-adapter.md`, `interfaces/streaming-api.md`, `adapter-checklist.md` |
| **streaming** | `services/streaming.md`, `interfaces/streaming-api.md` |
| **recording** | `services/recording.md` |
| **notifier** | `services/notifier.md`, `interfaces/web-api.md` |
| **web-backend** | `services/web-backend.md`, `interfaces/web-api.md`, `interfaces/mqtt-publisher-guide.md` |
| **web-frontend** | `services/web-frontend.md`, `interfaces/web-api.md` |

다른 서비스의 `services/<other>.md`는 읽지 마세요. 계약이 필요하면 `interfaces/`를 참조.

### (c) 운영자

1. `operational-rules.md` — 네이밍/설정/모니터링/리소스 정책
2. `services/<관심 서비스>.md` — 운영 중 문제 발생 시 해당 서비스만

### (d) orchestrator 세션 (사용자와 직접 대화)

1. `architecture-overview.md` — 시스템 전체 그림
2. `interfaces/*.md` 3종 — 서비스 간 계약
3. `services/*.md` — 필요 시 개별 서비스

> **중요:** `architecture-overview.md`는 **orchestrator 전용**입니다. 하위 Agent/Ralph 세션에 전달 금지.
> 구현을 위임할 때는 해당 `services/<name>.md` + 닿는 `interfaces/*.md`만 프롬프트에 명시하세요.
> (`~/projects/sentinel/CLAUDE.md` Doc Orchestration 규칙 참조)

## 3. 레거시 파일 상태

| 파일 | 상태 | 대체 |
|---|---|---|
| `docs/api-rest.md` | 2026-04 삭제됨 | `interfaces/web-api.md` |
| `docs/api-internal.md` | 2026-04 삭제됨 | `interfaces/web-api.md` |
| `docs/api-websocket.md` | 2026-04 삭제됨 | `interfaces/web-api.md` |

삭제 배경: 세 파일은 실제 구현과 drift된 aspirational 스펙이었고, `interfaces/web-api.md`가 실제 `services/web-backend/` 코드 기반 SSOT이다. 단일 SSOT 원칙에 따라 통합·제거함 (US-002).
