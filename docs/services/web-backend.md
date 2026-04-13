# web-backend

> **Reader scope:** 이 서비스를 구현·수정하는 Claude 세션 전용.
> 다른 서비스의 내부 구현을 읽지 마세요. 외부와의 계약은 아래 "Interfaces" 섹션의 링크만 참조.
> 시스템 전체 그림은 orchestrator 세션 영역(`docs/architecture-overview.md`)이며 본 세션은 읽을 필요 없음.

## Responsibility

중앙 REST API + WebSocket 서버. 모든 데이터(SQLite)와 인증을 책임지고, 클라이언트와 서비스 간의 조정을 담당한다. 다른 서비스로의 proxy(녹화/아카이브)와 장비 restart 경유도 여기서 처리한다.

## Interfaces

| Boundary | Direction | Spec |
|----------|-----------|------|
| web-frontend (REST + `/ws`) | inbound | 본 문서 "HTTP API" (공식 카탈로그는 별도 `docs/interfaces/web-api.md` 예정) |
| hw-gateway (`POST /api/incidents`) | inbound | 본 문서 "HTTP API" |
| notifier (`GET /api/contacts`, `POST /api/links/temp`, `POST /api/alarms`) | inbound | 본 문서 "HTTP API" |
| hw-gateway (restart command forward) | outbound | `POST {HW_GATEWAY_URL}/api/restart` |
| streaming (stream list) | outbound | `GET {STREAMING_URL}/api/streams` |
| cctv-adapter / youtube-adapter (reload) | outbound | `POST /api/cameras/reload` |
| recording (proxy) | outbound | 본 문서 "Outbound Calls" |

## Code Structure

Go 파일 분리: `services/web-backend/`
- `main.go` — 라우팅, middleware wiring, startup (약 190 lines)
- `auth.go` — login/register/approve/reject, JWT, password change
- `migrations.go` — SQLite 스키마 자동 마이그레이션
- `contacts.go` / `sites.go` / `cameras.go` / `incidents.go` / `invitations.go` / `settings.go` / `links.go` / `equipment.go` / `recordings.go` / `devices.go` / `health.go` — 리소스별 핸들러
- `health.go` — HealthMonitor goroutine: 서비스 `/healthz` 폴링 + sensor `last_seen` 평가. 상태 전이 시 `health_events` INSERT
- `websocket.go` — `/ws` crisis broadcast
- `ratelimit.go` — login/register limiter

라우팅 구조:
- **Public mux**: healthz, `/auth/*`, `/ws`, `/api/contacts`(GET only — notifier용), `/api/links/temp`, `/api/links/verify/{token}`, `/internal/*`(내부 서비스용), `/api/invitations/verify/{token}`, `POST /api/incidents`(hw-gateway용), `POST /api/devices/seen`(hw-gateway용 — device 자동 영속), `POST /api/incidents/{id}/resolve-from-sensor`(hw-gateway용 — 센서 버튼 양방향 해소).
- **`apiMux` (authMiddleware 적용)**: 나머지 `/api/*` 모두 (contacts CRUD, sites, cameras CRUD, incidents list/ack/resolve, equipment restart, recordings/archives proxy, settings, links 관리, devices 관리, `/api/health`, `/api/health/events`).

## Environment Variables

| Var | Meaning |
|-----|---------|
| `HW_GATEWAY_URL` | restart forward 대상 |
| `STREAMING_URL` | stream list 조회 |
| `CCTV_ADAPTER_URL` / `YOUTUBE_ADAPTER_URL` | 카메라 reload 트리거 |
| `NOTIFIER_URL` | (test-alert proxy) |
| `RECORDING_URL` | 녹화/아카이브 proxy 대상 |
| `FRONTEND_URL` | temp link URL 조립, CORS |

## Build & Run

```bash
docker compose build web-backend
docker compose up -d web-backend
docker compose logs -f web-backend
```

- 포트: 내부 8080
- 헬스: `GET /healthz`, `GET /api/healthz` (auth 통과 후)
- DB: volume `db-data` → `/data/sentinel.db` (SQLite)
- 첫 실행 시 내장 admin 계정 생성 (자격은 `ADMIN_USERNAME`/`ADMIN_PASSWORD` 환경변수, compose 기본값 확인)

## HTTP API (핵심만)

> 전체 카탈로그는 `docs/interfaces/web-api.md` 예정. 본 문서는 구조/대표 엔드포인트만.

### Public
| Method | Path | Purpose |
|--------|------|---------|
| POST | `/auth/login`, `/auth/register` | 로그인/가입 (rate-limited) |
| POST | `/auth/approve/{userId}`, `/auth/reject/{userId}` | 관리자 승인 |
| GET | `/auth/pending`, `/auth/users` | 유저 목록 |
| GET | `/ws` | WebSocket crisis push |
| POST | `/api/incidents` | hw-gateway용 crisis 기록 + WS broadcast |
| GET | `/api/contacts` | notifier용 (public GET; CUD는 auth 필요) |
| POST | `/api/links/temp` | temp JWT link 발급 |
| GET | `/api/links/verify/{token}` | temp link 검증 |
| GET | `/internal/cameras`, `/internal/settings/{key}` | 내부 서비스용 (cctv/recording이 카메라 목록 fetch) |

### Auth 필요 (`/api/*`)
contacts/sites/cameras CRUD, incidents list/ack/resolve, **devices list/alias/soft-delete/restore**, invitations, settings, links 관리, `POST /api/equipment/restart`(→ hw-gateway forward, devices 테이블 등록+미삭제 검증), `POST /api/test-alert`(→ notifier forward), recordings/archives/storage proxy(→ recording forward).

## Outbound Calls

- **hw-gateway** `POST /api/restart` — 장비 재시작 요청.
- **streaming** `GET /api/streams` — 카메라 HLS URL 조립 시.
- **cctv-adapter** / **youtube-adapter** `POST /api/cameras/reload` — 카메라 CRUD 시 reload 트리거.
- **recording** — `/api/recordings/*`, `/api/archives/*`, `/api/storage` 요청을 그대로 proxy (auth 통과 후).
- **notifier** `POST /api/notify` — test-alert 경로.

## Authentication

| Role | Method | Scope |
|------|--------|-------|
| Admin | JWT login | 전체 |
| User | JWT login (admin 승인 후) | 뷰 + 허용된 작업 |
| Temp Viewer | JWT temp link (24h) | `/view/{token}` CCTV만 |

- `authMiddleware` 가 `/api/*` 보호.
- Temp link는 DB 저장 없이 JWT. 폐기는 in-memory blacklist (`links.go` 확인).
- Rate limit: login/register만.

## Constraints / Known Issues

- SQLite 단일 파일. 동시 write가 많아지면 경합 — 현재 규모로는 충분.
- WebSocket은 push 전용. 재연결은 클라이언트 책임.
- `/internal/*`는 network 격리 전제 — 외부 노출 금지.
- Proxy 대상(recording 등)이 down이면 관련 API가 5xx — 타임아웃/에러 처리 일관성 유지.

## Storage / State

- **SQLite** `/data/sentinel.db` — 테이블: users, contacts, sites, cameras, incidents(+device_id, +resolved_by_kind/id/label for 양방향 attribution), invitations, settings, temp_links 메타, devices(site_id+device_id UNIQUE, soft delete via deleted_at), `health_events`(상태 전이 이력, entity_kind+entity_id+detected_at 인덱스) 등. 스키마는 `migrations.go` 참조 (SSOT).
- **In-memory (HealthMonitor)**: service registry당 entity 상태 캐시 (`status`, `lastCheck`, `since`, `consecutiveFailures`, `failingSince`, `lastDetail`). 컨테이너 재시작 시 휘발 (이력은 health_events에 영속).
- **In-memory**: temp link JWT blacklist, WebSocket client registry, rate limiter buckets.
