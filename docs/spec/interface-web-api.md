# Web API 인터페이스 스펙

> 상태: 살아있는 계약 (living spec) · 독자: 스펙 작성자 / 오케스트레이터
> 접합부: **web-backend ↔ web-frontend** (+ 내부 서비스 hw-gateway 호출부)
> 원본 SSOT: `docs/interfaces/web-api.md` — 본 문서는 이를 "계약 + 검증 단언" 형태로 승격한 것.

## 목적 / 의도

Sentinel 웹 계층의 HTTP/WebSocket 인터페이스를 **판정 가능한 계약**으로 고정한다.
web-frontend(및 모바일 브라우저, 임시 링크 뷰어)와 내부 서비스(hw-gateway)는 이 계약만 보고
web-backend와 통신할 수 있어야 하며, 각 계약의 검증 단언은 OK/NOK로 기계 판정 가능해야 한다.
개별 서비스 스펙(web-backend, web-frontend)은 이 문서를 접합부 SSOT로 참조한다.

## 언어 · 런타임

- **서버**: Go (표준 `net/http`, Go 1.22+ 메서드 패턴 라우팅), 포트 `:8080`
- **클라이언트**: TypeScript + React (Vite), 브라우저 `fetch` / `WebSocket`
- **직렬화**: JSON (`Content-Type: application/json`), WS는 JSON 텍스트 프레임

## 의존 도구 · 시스템

- **SQLite** — users, incidents, cameras, sites, contacts, devices, invitations, system_settings, health_events 영속
- **JWT (HS256)** — 로그인 토큰 24h, 임시 링크 토큰 24h, 초대 토큰 7일(64 hex, JWT 아님)
- **하위 서비스 프록시 대상**: recording(녹화/아카이브/스토리지), streaming(HLS 상태 병합), hw-gateway(재시작/테스트 알람), notifier(초대 이메일)
- **네트워크 전제**: `/internal/*` 및 무인증 internal 엔드포인트는 Docker 네트워크 격리 + 리버스 프록시(nginx) 차단이 전제

## 공통 규약 (모든 계약에 적용)

- 인증 스킴: `Authorization: Bearer <JWT>` · WS는 `?token=<jwt>` 쿼리 파라미터
- role: `admin` · `user` · `temp`(임시 링크, read-only)
- 성공: `200` / `201` / `204` · 에러 바디: `{"error": "<message>"}`
- 에러 코드 매핑: `400` 잘못된 입력 · `401` 인증 실패/만료 · `403` 권한 부족 · `404` 없음 · `409` 상태 충돌 · `429` 레이트 리밋(login/register 한정) · `502` 하위 서비스 통신 실패
- `/api/*` 경로는 authMiddleware 통과 필수(단, 무인증 internal 예외는 §계약 13에 명시). `/auth/pending|approve|reject|users`는 `/api/` 프리픽스가 아니며 핸들러가 직접 admin JWT를 검증

**공통 검증 단언**

- [ ] JWT 없이 `GET /api/cameras` 호출 → `401` + `{"error": ...}`
  ```bash
  curl -s -o /dev/null -w '%{http_code}' http://localhost:8080/api/cameras   # → 401
  ```
- [ ] user 토큰으로 admin 전용 엔드포인트(예: `GET /api/settings`) 호출 → `403`
- [ ] 모든 JSON 응답의 `Content-Type`이 `application/json` (HLS 프록시 제외)

---

## 계약 1: 인증 / 사용자 (`/auth/*`, `/api/auth/change-password`)

### 입력

| Method | Path | Auth | 바디 |
|--------|------|------|------|
| POST | `/auth/register` | public (5 req/min/IP) | `{username*, password*(≥8), confirmPassword*(=password), name*, inviteToken?}` |
| POST | `/auth/login` | public (10 req/min/IP) | `{username*, password*}` |
| POST | `/api/auth/change-password` | user | `{currentPassword*, newPassword*(≥8)}` |
| GET | `/auth/pending` | admin | — |
| POST | `/auth/approve/{userId}` | admin | — |
| POST | `/auth/reject/{userId}` | admin | — |
| GET | `/auth/users` | admin | — |

### 출력 (계약)

- `POST /auth/register` → `201` `{id, username, name, email, status: "pending"|"active"}` · `409` username/email 중복 · `400` 검증 실패
- `POST /auth/login` → `200` `{token: "<JWT>", user: {id, username, role: "user"|"admin"}}` · `401` 자격 불일치 · `403` `account pending approval`
- `POST /api/auth/change-password` → `200` `{"message": "password changed successfully"}`
- `GET /auth/pending` → `200` `[{id, username, name, email, status:"pending", createdAt}]`
- `POST /auth/approve/{userId}` → `200` `{"id", "status":"active"}` · reject는 `"rejected"`
- `GET /auth/users` → `200` `[{id, username, name, email, role, createdAt}]`
- 레이트 리밋 초과 → `429`

### 핵심 로직 (불변식)

- 가입 기본 상태는 `pending`; 유효한 `inviteToken` 제시 시 자동 `active` + 초대 이메일 주입
- `pending` 계정은 로그인 불가(`403`) — 승인 전 시스템 접근 경로 없음
- JWT 유효기간 24h, HS256. 토큰에 `role` 클레임 포함
- `/auth/pending|approve|reject|users`는 authMiddleware 밖 — 핸들러가 직접 admin JWT 검증 (프리픽스 `/api/` 아님이 계약)
- 레이트 리밋은 IP 단위, `/auth/login` 10/min, `/auth/register` 5/min — 다른 엔드포인트에는 적용하지 않음

### 검증 단언 (TDD)

- [ ] 정상 가입 → `201`, `status: "pending"`
  ```bash
  curl -s -X POST http://localhost:8080/auth/register \
    -d '{"username":"t1","password":"secret123","confirmPassword":"secret123","name":"T1"}'
  # → 201, body.status == "pending"
  ```
- [ ] 7자 비밀번호 가입 → `400`
- [ ] `pending` 계정 로그인 → `403` + error에 pending 명시
- [ ] 로그인 성공 → `200`, `token` 필드가 3-파트 JWT, `user.role ∈ {user, admin}`
  ```bash
  curl -s -X POST http://localhost:8080/auth/login -d '{"username":"admin","password":"<pw>"}' \
    | jq -e '.token | split(".") | length == 3'
  ```
- [ ] 같은 IP에서 `/auth/login` 11회 연속 → 11번째 `429`
- [ ] user 토큰으로 `GET /auth/pending` → `401` 또는 `403` (200 아님)
- [ ] `POST /auth/approve/{id}` 후 해당 계정 로그인 → `200`

---

## 계약 2: Incidents (`/api/incidents*`)

### 입력

| Method | Path | Auth | 입력 |
|--------|------|------|------|
| GET | `/api/incidents` | user | query: `page`(≥1, def 1) `limit`(def 20, max 100) `from` `to`(occurred_at, SQLite datetime) `status`(`open\|acknowledged\|resolved`) |
| PATCH | `/api/incidents/{id}/acknowledge` | admin | 바디 없음 |
| PATCH | `/api/incidents/{id}/resolve` | admin | `{resolutionNotes*: non-empty string}` |
| POST | `/api/incidents` | **none (internal)** | → 계약 13 |

### 출력 (계약)

`GET /api/incidents` → `200`:

```json
{
  "data": [{
    "id": 12, "siteId": "site-001", "description": "gas leak",
    "occurredAt": "2026-04-13 10:20:30",
    "confirmedAt": null, "confirmedBy": null, "isTest": false,
    "status": "open | acknowledged | resolved",
    "resolvedAt": null, "resolvedBy": null, "resolutionNotes": null,
    "resolvedByKind": null, "resolvedById": null, "resolvedByLabel": null
  }],
  "pagination": { "page": 1, "limit": 20, "total": 57 }
}
```

- `PATCH .../acknowledge` → `200` `{"status":"acknowledged"}` · 이미 `resolved`면 `409`
- `PATCH .../resolve` → `200` `{"status":"resolved", "resolvedByKind":"web", "resolvedById":"<username>", "resolvedByLabel":"..."}` · 이미 resolved면 `409` · notes 비어있으면 `400`

### 핵심 로직 (불변식)

- 상태 기계: `open → acknowledged → resolved` (resolved는 종단 — 재해결 `409`)
- **양방향 해소 attribution**: `resolvedByKind ∈ {"web", "sensor_button", null}` — 웹 해제는 본 계약, 센서 버튼 해제는 계약 13(`resolve-from-sensor`). 어느 경로든 동일 필드에 기록
- resolve 성공 부작용 3종: (a) recording 아카이브 finalize 비동기 트리거 (b) hw-gateway 경유 MQTT `safety/{siteId}/alert/resolved` 발행 (c) WS `incident_resolved` 브로드캐스트 (계약 14)
- `limit > 100` 요청은 100으로 클램프 (에러 아님)

### 검증 단언 (TDD)

- [ ] `GET /api/incidents?limit=500` → `200`, `pagination.limit == 100`
  ```bash
  curl -s -H "Authorization: Bearer $T" 'http://localhost:8080/api/incidents?limit=500' \
    | jq -e '.pagination.limit == 100'
  ```
- [ ] `status=resolved` 필터 → `data[]`의 모든 `status == "resolved"`
- [ ] open incident에 resolve(notes 있음) → `200`, `resolvedByKind == "web"`
- [ ] 같은 incident에 resolve 재호출 → `409`
- [ ] `resolutionNotes: ""` 로 resolve → `400`
- [ ] user 토큰으로 acknowledge → `403`
- [ ] resolve 성공 직후 WS 클라이언트가 `incident_resolved` 메시지 수신 (계약 14 단언과 교차)

---

## 계약 3: Cameras (`/api/cameras*`)

### 입력

| Method | Path | Auth | 바디 |
|--------|------|------|------|
| GET | `/api/cameras` | user | — |
| POST | `/api/cameras` | admin | `{name, location, zone, sourceType: "rtsp"\|"youtube", sourceUrl, enabled}` |
| PUT | `/api/cameras/{id}` | admin | 위와 동일, 비어있지 않은 필드만 적용 (partial) |
| DELETE | `/api/cameras/{id}` | admin | — |

### 출력 (계약)

Camera object:

```json
{
  "id": 1, "name": "Entry Cam", "location": "Main gate", "zone": "Zone A",
  "streamKey": "cam-a1b2c3d4",
  "sourceType": "rtsp | youtube", "sourceUrl": "rtsp://... | https://youtube.com/...",
  "enabled": true,
  "hlsUrl": "http://streaming/.../index.m3u8",
  "status": "connected | disconnected"
}
```

- GET → `200` 배열 · POST → `201` · PUT → `200` · DELETE → `204` · 검증 실패 `400`

### 핵심 로직 (불변식)

- `streamKey`는 생성 시 서버가 자동 발급하며 **불변** — PUT으로 변경 불가
- 입력 검증: `sourceType`은 `rtsp|youtube`만 · RTSP는 `rtsp://`/`rtsps://` 스킴 필수 · YouTube는 `https://(www.)youtube.com/watch?v=...` 또는 `https://youtu.be/...`만
- **SSRF 차단**: sourceUrl hostname이 loopback/private/link-local IP면 `400` 거절
- `hlsUrl`·`status`는 streaming 서비스에서 10초 캐시로 조회해 병합 (DB 저장값 아님)
- 생성/수정/삭제 시 cctv-adapter·youtube-adapter에 비동기 reload 트리거 (응답을 블로킹하지 않음)

### 검증 단언 (TDD)

- [ ] `sourceType: "http"` 생성 → `400`
- [ ] `sourceUrl: "rtsp://192.168.0.10/stream"` (private IP) 생성 → `400`
  ```bash
  curl -s -o /dev/null -w '%{http_code}' -X POST http://localhost:8080/api/cameras \
    -H "Authorization: Bearer $ADMIN" \
    -d '{"name":"x","sourceType":"rtsp","sourceUrl":"rtsp://127.0.0.1/s","enabled":true}'
  # → 400
  ```
- [ ] 정상 생성 → `201`, 응답에 `streamKey` 자동 포함
- [ ] PUT에 `streamKey` 포함 요청 → 저장된 streamKey 불변 (재조회로 확인)
- [ ] PUT에 `{"name":"새이름"}`만 → 다른 필드 유지 (partial update)
- [ ] DELETE → `204`, 이후 GET 목록에서 사라짐
- [ ] user 토큰으로 POST → `403`, GET은 `200`

---

## 계약 4: Sites (`/api/sites*`)

### 입력

| Method | Path | Auth | 바디 |
|--------|------|------|------|
| GET | `/api/sites` | admin | — |
| PUT | `/api/sites/{id}` | admin | `{address?, managerName?, managerPhone?}` (partial) |

### 출력 (계약)

```json
{ "id": 1, "address": "...", "managerName": "...", "managerPhone": "010-1234-5678" }
```

GET → `200` 배열 · PUT → `200` 갱신된 객체 · 포맷 위반 `400`

### 핵심 로직 (불변식)

- `managerPhone` 포맷: `01[016789]-\d{3,4}-\d{4}`
- 빈 문자열 필드는 무시 (partial update — 빈 값으로 덮어쓰지 않음)

### 검증 단언 (TDD)

- [ ] `managerPhone: "02-123-4567"` → `400`
- [ ] `{"managerPhone":""}` PUT → `200`, 기존 phone 유지
- [ ] user 토큰으로 GET → `403`

---

## 계약 5: Contacts (`/api/contacts*`)

### 입력

| Method | Path | Auth | 바디 |
|--------|------|------|------|
| GET | `/api/contacts` | user | — |
| POST | `/api/contacts` | admin | `{name, phone, email, notifyEmail}` |
| PUT | `/api/contacts/{id}` | admin | 동일 (partial) |
| DELETE | `/api/contacts/{id}` | admin | — |

### 출력 (계약)

```json
{ "id": 1, "name": "김관리", "phone": "010-1234-5678", "email": "manager@example.com", "notifyEmail": true }
```

GET → `200` 배열 · POST → `201` · PUT → `200` · DELETE → `204`

### 핵심 로직 (불변식)

- `phone` 포맷: `01[016789]-\d{3,4}-\d{4}`
- 내부 서비스(notifier 등)용 무인증 목록 조회는 `/internal/` 경로로 분리 제공 (계약 13, ⚠️ 리뷰 항목 1 참조)

### 검증 단언 (TDD)

- [ ] JWT 없이 `GET /api/contacts` → `401`
- [ ] 잘못된 phone으로 POST → `400`
- [ ] DELETE → `204`, 재삭제 → `404`

---

## 계약 6: Devices (`/api/devices*`)

### 입력

| Method | Path | Auth | 입력 |
|--------|------|------|------|
| GET | `/api/devices` | user | — (삭제 제외 목록) |
| GET | `/api/devices/all` | admin | — (soft-deleted 포함) |
| PATCH | `/api/devices/{id}` | user | `{alias: string}` |
| DELETE | `/api/devices/{id}` | user | — (soft delete) |
| POST | `/api/devices/{id}/restore` | user | — |

### 출력 (계약)

Device object:

```json
{
  "id": 1, "siteId": "site1", "deviceId": "PRESS-01", "alias": "1호 프레스",
  "firstSeen": "2026-04-13 10:20:30", "lastSeen": "2026-04-13 10:30:00",
  "deletedAt": null
}
```

- PATCH → `200` `{id, alias}` · 없으면 `404`
- DELETE → `204` · 이미 삭제/없음 `404`
- restore → `200` `{id, "status":"restored"}`
- `GET /api/devices/all`을 user가 호출 → `403` `{"error":"admin access required"}`

### 핵심 로직 (불변식)

- device 등록의 원천은 hw-gateway의 `POST /api/devices/seen` (계약 13) — 웹에서는 생성 불가, 수정(alias)/soft delete/복원만
- 삭제는 soft delete(`deletedAt`) — 이후 해당 device의 heartbeat/alert가 다시 오면 **자동 복원**
- `GET /api/devices`는 `deletedAt IS NULL`만 반환

### 검증 단언 (TDD)

- [ ] user 토큰으로 `GET /api/devices/all` → `403`
- [ ] DELETE 후 `GET /api/devices`에 미포함, `GET /api/devices/all`(admin)에는 `deletedAt` 채워져 포함
- [ ] soft-deleted device에 `POST /api/devices/seen` (계약 13) → 이후 `GET /api/devices`에 재등장 (`deletedAt == null`)
- [ ] 존재하지 않는 id에 PATCH → `404`

---

## 계약 7: Equipment / Test Alert (`/api/equipment/restart`, `/api/test-alert`)

### 입력

| Method | Path | Auth | 바디 |
|--------|------|------|------|
| POST | `/api/equipment/restart` | user | `{siteId*, deviceId*, reason?}` |
| POST | `/api/test-alert` | admin | 없음 |

### 출력 (계약)

- restart: hw-gateway 응답을 상태코드 포함 그대로 중계 · hw-gateway 통신 실패 `502` · 미등록/삭제된 device `400`
- test-alert: hw-gateway `/api/test-alert`에 `{siteId:"test", deviceId:"TEST-DEVICE"}` 전달, 응답 중계

### 핵심 로직 (불변식)

- restart 전제조건: `deviceId`가 devices 테이블에 존재하고 `deleted_at IS NULL` — 최초 heartbeat 수신 전에는 재시작 거부 (`400`)
- web-backend는 순수 프록시 — hw-gateway의 응답 바디/상태코드를 변형하지 않음

### 검증 단언 (TDD)

- [ ] 미등록 deviceId로 restart → `400` (hw-gateway 호출 자체가 발생하지 않음)
  ```bash
  curl -s -o /dev/null -w '%{http_code}' -X POST http://localhost:8080/api/equipment/restart \
    -H "Authorization: Bearer $T" -d '{"siteId":"site1","deviceId":"NEVER-SEEN"}'   # → 400
  ```
- [ ] hw-gateway 다운 상태에서 등록된 device restart → `502`
- [ ] user 토큰으로 `/api/test-alert` → `403`

---

## 계약 8: Recordings / Archives / Storage (recording 서비스 프록시)

### 입력

| Method | Path | Auth |
|--------|------|------|
| GET | `/api/recordings/{stream_key}` | user |
| GET | `/api/recordings/{stream_key}/play` | user (query `from`, `to`) |
| GET | `/api/recordings/{stream_key}/segments/{filename}` | user |
| GET | `/api/archives` | user |
| POST | `/api/archives` | user |
| DELETE | `/api/archives/{id}` | user |
| DELETE | `/api/archives/incident/{incidentId}` | user |
| GET | `/api/archives/{id}/download` | user |
| GET | `/api/storage` | user |

### 출력 (계약)

- 요청/응답 바디는 recording 서비스 원본을 그대로 통과 (스키마 SSOT는 recording 서비스 문서 — 본 계약 범위 밖)
- HLS 응답(`application/vnd.apple.mpegurl`)과 다운로드 바이너리 스트림은 Content-Type 보존하여 forward
- recording 서비스 통신 실패 → `502`

### 핵심 로직 (불변식)

- 이 그룹에서 web-backend가 보장하는 것은 **인증 게이트 + 투명 프록시** 두 가지뿐 — 바디 변형·필터링 없음
- JWT 없이는 어떤 프록시 경로도 통과 불가

### 검증 단언 (TDD)

- [ ] JWT 없이 `GET /api/storage` → `401` (프록시 이전 차단)
- [ ] recording 컨테이너 중지 후 `GET /api/archives` → `502`
- [ ] `GET /api/recordings/{key}/play` 응답 Content-Type이 recording 서비스 응답과 동일 (m3u8 보존)

---

## 계약 9: Links — 임시 공유 링크 (`/api/links*`)

### 입력

| Method | Path | Auth | 바디 |
|--------|------|------|------|
| POST | `/api/links/temp` | admin **또는** internal(Authorization 헤더 없으면 Docker 내부 호출로 간주) | `{label?: string}` |
| GET | `/api/links/verify/{token}` | public | — |
| GET | `/api/links` | admin | — |
| DELETE | `/api/links/{id}` | admin | — |

### 출력 (계약)

- POST → `201`:
  ```json
  { "id": "uuid", "token": "<JWT>", "url": "http://<site_url>/view/<token>", "expiresAt": "2026-04-14T10:20:30Z" }
  ```
- verify → `200` `{"valid": true, "expiresAt": "..."}` · 만료/회수 `401`
- GET 목록 → `200` `[{id, label, createdAt, expiresAt}]` (활성만 — 만료·회수 제외)
- DELETE → `204`

### 핵심 로직 (불변식)

- 만료 24시간 고정. temp JWT의 role은 `temp` (read-only)
- `url`의 호스트: system_settings `site_url` 우선, 없으면 `FRONTEND_URL` env
- 회수(DELETE)는 **블랙리스트 방식** — JWT 서명 자체는 유효하지만 서버가 거부. 회수된 토큰으로 WS 접속(계약 14)·verify 모두 실패해야 함
- Authorization 헤더가 **있으면** 반드시 유효한 admin JWT — 잘못된 헤더는 internal로 폴백되지 않고 거절

### 검증 단언 (TDD)

- [ ] admin으로 생성 → `201`, `url`에 `/view/<token>` 포함
- [ ] user 토큰으로 생성 → `403` (internal 폴백 아님)
- [ ] 발급 직후 verify → `200 {"valid":true}`
- [ ] DELETE 후 같은 token으로 verify → `401`
  ```bash
  ID=$(curl -s -X POST http://localhost:8080/api/links/temp -H "Authorization: Bearer $ADMIN" -d '{}' | jq -r .id)
  curl -s -X DELETE http://localhost:8080/api/links/$ID -H "Authorization: Bearer $ADMIN"   # → 204
  # 이후 verify → 401
  ```
- [ ] DELETE 후 `GET /api/links` 목록에서 제외

---

## 계약 10: Invitations (`/api/invitations*`)

### 입력

| Method | Path | Auth | 바디 |
|--------|------|------|------|
| POST | `/api/invitations` | admin | `{email*: string}` |
| GET | `/api/invitations` | admin | — |
| DELETE | `/api/invitations/{id}` | admin | — (pending만) |
| GET | `/api/invitations/verify/{token}` | public | — |

### 출력 (계약)

- POST → `201` `{id, email, token: "<64 hex>", status:"pending", createdAt, expiresAt}`
- GET → `200` 배열 — pending이지만 만료된 항목은 `status: "expired"`로 변환하여 반환
- DELETE → `204` · pending 아니면 `404`
- verify → `200` `{"email":"...","status":"valid"}` · 만료/상태 이상 `400` · 없음 `404`

### 핵심 로직 (불변식)

- 유효기간 7일. 생성 시 notifier 경유 `<site_url>/register?invite=<token>` 링크 이메일 **비동기** 발송 (발송 실패가 201을 막지 않음)
- 유효한 초대 토큰으로 가입(계약 1) 시 자동 `active` + 초대 email 주입
- 취소는 pending 상태에서만 가능

### 검증 단언 (TDD)

- [ ] 생성 → `201`, `token`이 64자 hex (`jq -e '.token | test("^[0-9a-f]{64}$")'`)
- [ ] verify(유효) → `200`, `email` 일치
- [ ] DELETE 후 verify → `400` 또는 `404` (200 아님)
- [ ] 초대 토큰으로 가입 → `201`, `status == "active"` (계약 1과 교차)

---

## 계약 11: Settings (`/api/settings*`)

### 입력

| Method | Path | Auth | 바디 |
|--------|------|------|------|
| GET | `/api/settings` | admin | — |
| PUT | `/api/settings/{key}` | admin | `{value: string}` |

### 출력 (계약)

- GET → `200` `[{"key":"site_url","value":"https://...","updatedAt":"..."}]`
- PUT → `200` `{key, value, updatedAt}` · 없는 key `404`

### 핵심 로직 (불변식)

- PUT은 **기존 key만** 갱신 — 새 key 생성 불가 (`404`)
- 알려진 key: `site_url` · `health.service_check_interval_sec`(def 30) · `health.service_down_threshold_sec`(def 90) · `health.sensor_alive_threshold_sec`(def 60)
- `site_url` 변경은 임시 링크 URL(계약 9)과 초대 이메일 링크(계약 10)에 즉시 반영

### 검증 단언 (TDD)

- [ ] `PUT /api/settings/nonexistent_key` → `404`
- [ ] `PUT /api/settings/site_url` `{"value":"https://x.example"}` → `200` · 이후 temp link 생성 시 `url`이 `https://x.example/view/...`
- [ ] user 토큰으로 GET → `403`

---

## 계약 12: Health — 통합 시스템 상태 (`/api/health*`)

### 입력

| Method | Path | Auth | 입력 |
|--------|------|------|------|
| GET | `/api/health` | user | — |
| GET | `/api/health/events` | user | query: `limit`(def 50) `offset`(def 0) `entity_kind`(`service`\|`sensor`) |

### 출력 (계약)

`GET /api/health` → `200` 배열:

```json
{
  "kind": "service | sensor",
  "id": "hw-gateway | site1:VOICE-01",
  "name": "hw-gateway | 음성센서 1",
  "status": "healthy | unhealthy",
  "lastCheck": "2026-04-13T06:13:41Z",
  "since": "2026-04-13T06:08:06Z",
  "detail": "no heartbeat 95s",
  "source": "docker-healthcheck-poll | mqtt-heartbeat"
}
```

`GET /api/health/events` → `200` `[{id, entityKind, entityId, status, detectedAt, detail}]`

### 핵심 로직 (불변식)

- 판정: service는 연속 실패가 `health.service_down_threshold_sec` 이상 지속되어야 unhealthy 확정(깜빡임 무시), 성공 1회면 즉시 healthy · sensor는 `now - last_seen > health.sensor_alive_threshold_sec`이면 unhealthy
- sensor `id`는 `siteId:deviceId`, `name`은 alias 우선(없으면 device_id)
- 모니터 제외: web-backend 자기 자신, mosquitto(HTTP healthz 없음)
- events는 **상태 전이 시점에만** 기록 (무변화 미기록 — 테이블 폭증 방지)

### 검증 단언 (TDD)

- [ ] `GET /api/health` 응답의 모든 항목이 `kind ∈ {service, sensor}`, `status ∈ {healthy, unhealthy}`
  ```bash
  curl -s -H "Authorization: Bearer $T" http://localhost:8080/api/health \
    | jq -e 'all(.[]; (.kind=="service" or .kind=="sensor") and (.status=="healthy" or .status=="unhealthy"))'
  ```
- [ ] 응답에 `id == "web-backend"`인 service 항목 없음
- [ ] `entity_kind=sensor` 필터 → 모든 `entityKind == "sensor"`
- [ ] 임의 서비스 컨테이너 중지 → threshold 경과 후 해당 항목 `unhealthy` + events에 전이 1행 추가 · 재시작 → healthy 전이 1행 추가 (전이당 정확히 1행)

---

## 계약 13: Internal — 무인증, Docker 네트워크 한정

외부 클라이언트 호출 금지. 리버스 프록시에서 차단이 배포 전제.

### 입력

| Method | Path | 호출자 | 바디 |
|--------|------|--------|------|
| GET | `/healthz` | Docker healthcheck | — |
| GET | `/api/healthz` | (JWT) 진단용 | — |
| GET | `/internal/cameras` | cctv-adapter | — |
| GET | `/internal/settings/{key}` | 타 서비스 | — |
| POST | `/api/incidents` | hw-gateway | `{siteId*, description, occurredAt?, isTest?}` |
| POST | `/api/devices/seen` | hw-gateway | `{siteId*, deviceId*}` |
| POST | `/api/incidents/{id}/resolve-from-sensor` | hw-gateway | 아래 참조 |

`resolve-from-sensor` 바디:

```json
{
  "incidentId": 12345,
  "siteId": "site1",
  "resolvedAt": "2026-04-13T10:30:00Z",
  "resolvedBy": { "kind": "sensor_button", "id": "VOICE-01", "label": "VOICE-01 reset 버튼" },
  "originalAlert": { "type": "scream", "deviceId": "PRESS-01" }
}
```

### 출력 (계약)

- `GET /healthz` → `200` `{"status":"ok","service":"web-backend"}`
- `POST /api/incidents` → `201` `{id, siteId, description, occurredAt}` · `siteId` 누락 `400`
- `POST /api/devices/seen` → `200` `{"status":"ok"}`
- `resolve-from-sensor` → `200` `{"status":"resolved","incidentId",  "resolvedByKind":"sensor_button","resolvedById","resolvedByLabel"}` · 매칭 미해결 incident 없음 `404` · 이미 resolved `409`(중복 버튼 방어) · `siteId` 누락 `400`

### 핵심 로직 (불변식)

- `POST /api/incidents` 성공 부작용: 전체 WS 클라이언트에 `crisis_alert` 브로드캐스트
- `POST /api/devices/seen`: `(site_id, device_id)` UPSERT — 없으면 INSERT(first_seen=last_seen=now), 있으면 `last_seen=now` + `deleted_at=NULL`(soft-delete 자동 복원). **멱등**
- `resolve-from-sensor` incident 매칭 폴백 체인: path `{id}`(0 허용) → body `incidentId` → 둘 다 0이면 `siteId`의 가장 최근 미해결 incident 자동 매칭
- `kind == "web"` echo는 hw-gateway 측에서 차단되어 이 엔드포인트에 도달하지 않는 것이 시스템 전제
- 성공 부작용: attribution 기록 + WS `incident_resolved` 브로드캐스트 + 아카이브 finalize 비동기 트리거

### 검증 단언 (TDD)

- [ ] `GET /healthz` (무인증) → `200 {"status":"ok","service":"web-backend"}`
- [ ] `POST /api/devices/seen` 동일 바디 2회 → 둘 다 `200`, device 행은 1개 (멱등)
- [ ] `POST /api/incidents` (siteId 있음) → `201` + 접속 중 WS 클라이언트에 `crisis_alert` 도착
- [ ] `POST /api/incidents/0/resolve-from-sensor` + body `incidentId: 0` + 유효 `siteId` → 해당 site 최신 미해결 incident가 resolved, `resolvedByKind == "sensor_button"`
- [ ] 같은 요청 재전송 → `409`
- [ ] **배포 게이트**: 외부(리버스 프록시 경유)에서 `POST /api/devices/seen` → `4xx` (통과 시 NOK — 네트워크 격리 실패)

---

## 계약 14: WebSocket (`/ws`)

### 입력

- URL: `ws(s)://<host>/ws?token=<jwt>` — 일반 JWT 또는 temp link JWT
- 클라이언트→서버 메시지: 읽히지만 무시됨 (keep-alive 용도만)

### 출력 (계약)

메시지 envelope (서버→클라 단방향):

```json
{ "type": "string", "payload": { }, "timestamp": "2026-04-13T10:20:30Z" }
```

| type | 수신 대상 | payload |
|------|----------|---------|
| `connected` | 접속 본인 | `{userId, role, connectedAt}` |
| `crisis_alert` | 전체 (admin/user/temp) | `{incidentId, siteId, description, occurredAt, isTest, site:{address,managerName,managerPhone}}` |
| `incident_resolved` | 전체 (admin/user/temp) | `{incidentId, siteId, resolvedAt, resolvedByKind, resolvedById, resolvedByLabel}` |
| `system_alarm` | admin 전용 | 임의 payload (송신 트리거 미확정 — TBD) |

### 핵심 로직 (불변식)

- 인증 실패(토큰 없음/만료/회수) 시 업그레이드 자체를 `401`로 거절
- 서버가 30초마다 ping — 클라이언트는 40초 내 pong 없으면 연결 종료
- role은 JWT에서 추출 (`admin`/`user`/`temp`) — `system_alarm`은 admin에게만 전달
- `crisis_alert`는 `POST /api/incidents`(계약 13) 성공 시, `incident_resolved`는 웹 resolve(계약 2) 또는 센서 resolve(계약 13) 성공 시 발생

### 검증 단언 (TDD)

- [ ] 토큰 없이 `/ws` 업그레이드 시도 → `401` (연결 수립 안 됨)
- [ ] 유효 JWT로 접속 → 첫 메시지 `type == "connected"`, `payload.role`이 토큰 role과 일치
- [ ] temp link 토큰으로 접속 → 성공, `payload.role == "temp"`
- [ ] 회수된(계약 9 DELETE) temp 토큰으로 접속 → `401`
- [ ] `POST /api/incidents` → 접속 중인 admin/user/temp 클라이언트 **모두** `crisis_alert` 수신
- [ ] user 클라이언트는 `system_alarm`을 수신하지 않음
- [ ] pong 미응답 클라이언트가 약 40초 후 서버에 의해 연결 종료

---

## ⚠️ 리뷰 필요 (문서-코드 불일치)

원본 `docs/interfaces/web-api.md`와 실제 라우트/핸들러 대조에서 발견된 어긋남. 본 스펙 본문에는 반영하지 않고 여기에만 기록한다.

1. **contacts 무인증 조회 경로가 문서와 다름** — 문서(§6 주의, §14 TBD)는 "`GET /api/contacts`가 `mux`와 `apiMux` 양쪽에 중복 등록"이라 하나, 실제 코드는 무인증 mux 쪽 등록이 `GET /internal/contacts`로 분리되어 있음(중복 등록 아님). 또한 문서 §12 Internal 표에 `/internal/contacts`가 누락되어 있다. → 문서의 중복 등록 주의문 삭제 + Internal 표에 `GET /internal/contacts` 추가 필요. (본 스펙 계약 13 표에는 코드 기준으로 `/internal/contacts`를 반영해 두었으나, 문서 SSOT 갱신 확인 필요)

2. **`POST /api/incidents` 요청 바디에 미문서 필드 존재** — 코드 핸들러는 `alertId`(존재 시 `incidents.alert_id` 기준 **dedup** — 동일 alertId 재전송이면 기존 incident를 반환하고 새로 만들지 않음)와 `deviceId`(best-effort device UPSERT 트리거)를 추가로 수용한다. 문서 §3은 `{siteId, description, occurredAt, isTest}`만 기술. → 멱등성(dedup) 계약이 문서에 빠져 있으므로 hw-gateway 접합부 관점에서 문서 보강 필요.
