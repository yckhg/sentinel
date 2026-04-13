# web-frontend

> **Reader scope:** 이 서비스를 구현·수정하는 Claude 세션 전용.
> 다른 서비스의 내부 구현을 읽지 마세요. 외부와의 계약은 아래 "Interfaces" 섹션의 링크만 참조.
> 시스템 전체 그림은 orchestrator 세션 영역(`docs/architecture-overview.md`)이며 본 세션은 읽을 필요 없음.

## Responsibility

모바일 우선 React SPA. CCTV multi-view, incident 히스토리, 관리 기능, crisis 실시간 배너, 뷰어 전용 페이지(`/view/{token}`)를 제공한다. 정적 빌드 산출물은 nginx로 서빙되며, 같은 nginx가 `/live/`(HLS)와 `/api/`, `/ws`를 backend/streaming으로 proxy한다.

## Interfaces

| Boundary | Direction | Spec |
|----------|-----------|------|
| web-backend (REST + WS) | outbound | [interfaces/web-api.md](../interfaces/web-api.md) — 본 한 장으로 호출 가능. backend 코드 직접 참조 금지 |
| web-backend (`/ws` WebSocket) | inbound | crisis alert push (`useWebSocket` 훅) |
| streaming (HLS `/live/*`) | inbound | [../interfaces/streaming-api.md](../interfaces/streaming-api.md) — HLS 규격. nginx proxy 경유 |
| 브라우저 Geolocation API | outbound | 119 버튼 |

## Code Structure

`services/web-frontend/`:
- `index.html`, `vite.config.ts`, `package.json` — Vite + React + TypeScript.
- `nginx.conf` — 정적 파일 + `/api/`, `/ws`, `/live/` proxy. 배포 시 런타임 라우팅 SSOT.
- `src/main.tsx`, `src/App.tsx` — React 진입점, 라우팅.
- `src/pages/`:
  - `LoginPage.tsx`, `CCTVPage.tsx`, `IncidentsPage.tsx`, `ManagementPage.tsx`, `SettingsPage.tsx`, `ViewerPage.tsx`
- `src/components/`:
  - `HLSPlayer.tsx` — hls.js wrapper (Safari는 native)
  - `CrisisAlertBanner.tsx` — 상단 persistent 배너
  - `EmergencyCallButton.tsx` — 119 + Geolocation
  - `RestartDialog.tsx` — 2-step 확인
  - `RecordingTimeline.tsx`, `DualCalendar.tsx` — 녹화 재생 UI
  - `DevicesSection.tsx` — Management 탭 내 장비(센서) 목록/별칭 편집/soft delete/복원, 10초 폴링
  - `HealthPanel.tsx` — Management 탭 상단 통합 health 패널 (services + sensors). `/api/health` 15초 폴링, status badge, 클릭 시 최근 health_events 모달
- `src/hooks/useWebSocket.ts` — WS 연결 + exponential backoff 재접속
- `src/utils/fetchWithTimeout.ts`, `isTokenExpired.ts`

## Environment Variables

빌드 타임만 사용. 런타임은 nginx proxy에 의존 (절대 URL 하드코딩 금지).

## Build & Run

```bash
docker compose build web-frontend
docker compose up -d web-frontend
docker compose logs -f web-frontend
```

- 포트: `3080:80` (유일한 외부 노출 포트). `yc-network`에도 조인되어 gateway proxy 대상이 될 수 있음.
- 헬스: `GET /healthz` (nginx static)
- 개발 시 로컬 iteration: compose로 빌드/재기동이 표준. 호스트에서 `npm` 직접 실행 금지(host protection 룰).

## Pages & Routes

URL 라우팅과 탭 전환이 혼합. **URL 라우팅(App.tsx 직접 분기)은 3가지뿐:**

| Route | Renders | Auth |
|-------|---------|------|
| `/view/{token}` | ViewerPage (외부 임시 링크 진입) | JWT temp link |
| `/register` | RegisterPage | None |
| `/` (그 외 모든 경로) | App + 하단 탭바 (로그인 필요) | JWT |

App 진입 후 탭은 **상태 기반**(`activeTab` state, URL 변경 없음):

| Tab | Component |
|-----|-----------|
| `cctv` (기본) | CCTVPage |
| `incidents` | IncidentsPage |
| `management` | ManagementPage |
| `settings` | SettingsPage |

미인증 시 LoginPage 렌더(URL은 `/` 유지). 탭 전환에 URL 변경이 없으므로 새로고침 시 항상 기본 탭으로 시작 — 라우팅 추가 시 이 점 고려.

## Outbound Calls

모든 API 호출은 web-backend로. 대표 사용 지점:
- **로그인/가입**: `POST /auth/login`, `POST /auth/register`
- **CCTVPage**: `GET /api/cameras` (목록) → 각 카메라 `hlsUrl`은 상대 경로 그대로 `<HLSPlayer src=...>`
- **IncidentsPage**: `GET /api/incidents`, `PATCH /api/incidents/{id}/acknowledge`, `/resolve`
- **ManagementPage**: contacts/sites/cameras/invitations/links/devices CRUD
  - HealthPanel (상단): `GET /api/health` (15초 폴링), `GET /api/health/events?limit=20` (모달 진입 시) — `src/components/HealthPanel.tsx`
  - Devices 섹션: `GET /api/devices`, `GET /api/devices/all`, `PATCH /api/devices/{id}`, `DELETE /api/devices/{id}`, `POST /api/devices/{id}/restore` — 10초 폴링, 클라이언트에서 `now - lastSeen < 30s` 기준으로 alive 상태 계산 (`src/components/DevicesSection.tsx`)
- **SettingsPage**: `GET/PUT /api/settings/{key}`, `POST /api/auth/change-password`, Health 임계값 3개(`health.service_check_interval_sec`, `health.service_down_threshold_sec`, `health.sensor_alive_threshold_sec`) 입력 — admin only
- **RestartDialog**: `POST /api/equipment/restart`
- **RecordingTimeline**: `GET /api/recordings/{key}`, `GET /api/recordings/{key}/play?from=&to=`, `GET /api/archives`, `POST /api/archives`
- **CrisisAlertBanner**: `useWebSocket` 훅이 `/ws`로 연결, crisis 메시지 수신 시 배너 렌더.
- **ViewerPage**: `GET /api/links/verify/{token}` → 통과 시 해당 카메라 HLS 재생.

백엔드 내부 구현은 모름. 응답 shape 변경은 `interfaces/web-api.md`에서 확인.

## Key UI Behaviors

- **Crisis alert**: 화면 상단 full-width persistent 배너. dismiss 전까지 유지. 다중 동시 crisis 시 누적 또는 최신 우선(구현 확인).
- **CCTV multi-view**: 그리드 레이아웃, 탭으로 확대.
- **119 버튼**: `navigator.geolocation.getCurrentPosition` 후 확인 다이얼로그.
- **Restart**: 1차 "정말로?" → 2차 사유 입력 → `POST /api/equipment/restart`.
- **Viewer page**: 네비게이션/관리 UI 없음. 토큰이 인증이다.

## Constraints / Known Issues

- 미니 PC 서빙 → 번들 크기 관리 필요. 큰 라이브러리(특히 hls.js 외) 도입 신중.
- Safari는 HLS native 지원 (hls.js 불필요). HLSPlayer가 분기해야 함.
- WS 재연결 backoff는 `useWebSocket.ts`에서 관리. 너무 짧으면 서버 부하, 너무 길면 crisis 놓칠 수 있음.
- 절대 URL 하드코딩 금지 — 모두 상대 경로 (nginx가 proxy).
- JWT 만료 처리: `isTokenExpired.ts` + 요청 실패 시 login 리다이렉트.

## Storage / State

- 런타임: `localStorage`에 JWT, 가벼운 UI 상태. Redux 등 전역 store 없음 (필요 시 컴포넌트 상태 + React Context).
- 영구 저장 없음 (SPA).
