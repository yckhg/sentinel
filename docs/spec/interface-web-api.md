# Web API 인터페이스 스펙

> 상태: 살아있는 계약 (living spec) · 독자: 스펙 작성자 / 오케스트레이터
> 접합부: **web-backend ↔ web-frontend** (+ 내부 서비스 HTTP: web-backend ↔ hw-gateway 계약 15, web-backend ← notifier 계약 13의 `/internal/*`)
> 본 문서가 Web API 접면의 SSOT다. MQTT 접면은 `docs/spec/interface-mqtt.md`, 스트리밍 접면은 `docs/spec/interface-streaming.md` 참조.

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
- **하위 서비스 프록시 대상**: recording(녹화/아카이브/스토리지), streaming(HLS 상태 병합), hw-gateway(재시작/테스트 알람/알람 해소 MQTT 발행 — 계약 15), notifier(초대 이메일)
- **네트워크 전제**: `/internal/*` 및 무인증 internal 엔드포인트의 보호는 Docker 네트워크 격리에 의존 — 리버스 프록시 레벨 차단은 현재 스택에 구성되어 있지 않음 (⚠️ 리뷰 항목 3 참조)

## 공통 규약 (모든 계약에 적용)

- 인증 스킴: `Authorization: Bearer <JWT>` · WS는 `?token=<jwt>` 쿼리 파라미터
- role: `admin` · `user` · `temp`(임시 링크, read-only)
- 성공: `200` / `201` / `204` · 에러 바디: `{"error": "<message>"}`
- 에러 코드 매핑: `400` 잘못된 입력 · `401` 인증 실패/만료 · `403` 권한 부족 · `404` 없음 · `409` 상태 충돌 · `429` 레이트 리밋(login/register 한정) · `502` 하위 서비스 통신 실패
- `/api/*` 경로는 인증 미들웨어 통과 필수(단, 무인증 internal 예외는 §계약 13에 명시). `/auth/pending|approve|reject|users`는 `/api/` 프리픽스가 아니며 핸들러가 직접 admin JWT를 검증

**공통 검증 단언**

- [ ] JWT 없이 `GET /api/cameras` 호출 → `401` + `{"error": ...}`
  ```bash
  curl -s -o /dev/null -w '%{http_code}' http://localhost:8080/api/cameras   # → 401
  ```
- [ ] user 토큰으로 admin 전용 엔드포인트(예: `GET /api/settings`) 호출 → `403`
- [ ] 모든 JSON 응답의 `Content-Type`이 `application/json` — 단 HLS 프록시(계약 8)와 두 `GET /healthz`(계약 13·15)는 제외. healthz 두 곳은 JSON 텍스트 바디를 Content-Type 미설정으로 raw 전송해 `text/plain; charset=utf-8`로 응답 (실측)
  ```bash
  curl -sI http://localhost:8080/healthz | grep -i '^content-type: text/plain'   # → 매치 (실측 동작)
  ```

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
- `/auth/pending|approve|reject|users`는 인증 미들웨어 밖 — 핸들러가 직접 admin JWT 검증 (프리픽스 `/api/` 아님이 계약)
- 레이트 리밋은 IP 단위, `/auth/login` 10/min, `/auth/register` 5/min — 다른 엔드포인트에는 적용하지 않음
- **비밀번호 변경은 기존 발급 토큰을 무효화** — `POST /api/auth/change-password` 성공 시 해당 사용자에게 변경 이전에 발급된 모든 로그인 JWT가 즉시 `401`이 된다(관측 가능한 자격증명 변경 경계 이후 재로그인 토큰만 유효 — 구현 방식은 계약 아님). 변경에 사용한 토큰 자신도 이후 무효가 되어 클라이언트는 재로그인해야 한다. 비밀번호 미변경 사용자의 토큰은 만료(24h)까지 유효

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
- [ ] 로그인 토큰 `t`로 `GET /api/incidents` → `200`; `POST /api/auth/change-password` 성공 후 **같은 `t`**로 `GET /api/incidents` → `401`; 변경 후 재로그인으로 얻은 새 토큰 → `200`

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
- resolve 성공 부작용 3종: (a) recording 아카이브 finalize 비동기 트리거 (b) hw-gateway `/api/alert/resolved`(계약 15) 경유 MQTT `safety/{siteId}/alert/resolved` 발행 (c) WS `incident_resolved` 브로드캐스트 (계약 14)
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
  "hlsUrl": "/live/cam-a1b2c3d4/index.m3u8",
  "status": "connected | disconnected"
}
```

- GET → `200` 배열 · POST → `201` · PUT → `200` · DELETE → `204` · 검증 실패 `400`

### 핵심 로직 (불변식)

- `streamKey`는 생성 시 서버가 자동 발급하며 **불변** — PUT으로 변경 불가
- 입력 검증: `sourceType`은 `rtsp|youtube`만 · RTSP는 `rtsp://`/`rtsps://` 스킴 필수 · YouTube는 `https://(www.)youtube.com/watch?v=...` · `https://(www.)youtube.com/live/...` · `https://youtu.be/...` 세 형태 허용
- **SSRF 차단**: sourceUrl hostname이 loopback/private/link-local IP면 `400` 거절
- `hlsUrl`·`status`는 streaming 서비스에서 10초 캐시로 조회해 병합 (DB 저장값 아님) — 활성 스트림이면 `hlsUrl`은 `/live/{streamKey}/index.m3u8` **상대 경로** + `status: "connected"`, 비활성이면 `hlsUrl: ""` + `status: "disconnected"`
- `hlsUrl`은 streaming이 반환한 상대 경로를 무변형 병합 — Docker 내부 절대 URL(`http://streaming/...`)은 어떤 경로로도 노출되지 않음 (상대 URL 정책 SSOT: `docs/spec/interface-streaming.md` 계약 2·4)
- 생성/수정/삭제 시 cctv-adapter·youtube-adapter에 비동기 reload 트리거 (응답을 블로킹하지 않음)

### 검증 단언 (TDD)

- [ ] `sourceType: "http"` 생성 → `400`
- [ ] `sourceType: "youtube"` + `sourceUrl: "https://youtube.com/live/abc123"` 생성 → `201`
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
- [ ] `GET /api/cameras` 응답에서 비어있지 않은 모든 `hlsUrl`이 `/live/`로 시작하고 `http`를 포함하지 않음 (interface-streaming.md 단언 A4-4와 교차)
  ```bash
  curl -s -H "Authorization: Bearer $T" http://localhost:8080/api/cameras \
    | jq -e 'all(.[]; .hlsUrl == "" or (.hlsUrl | startswith("/live/") and (contains("http") | not)))'
  ```

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
| PUT | `/api/contacts/{id}` | admin | 동일 — `name`/`phone`은 partial(빈 값 무시), `notifyEmail`은 생략 시 유지, **`email`은 partial 아님**(항상 전송값으로 덮어씀 — 생략/빈 값이면 NULL 저장) |
| DELETE | `/api/contacts/{id}` | admin | — |

### 출력 (계약)

```json
{ "id": 1, "name": "김관리", "phone": "010-1234-5678", "email": "manager@example.com", "notifyEmail": true }
```

GET → `200` 배열 · POST → `201` · PUT → `200` · DELETE → `204`

### 핵심 로직 (불변식)

- `phone` 포맷: `01[016789]-\d{3,4}-\d{4}`
- PUT의 필드별 갱신 시맨틱은 균일하지 않음 (실측): `name`·`phone`은 빈 값이면 기존 값 유지, `notifyEmail`은 필드 생략 시 기존 값 유지, `email`은 요청값으로 무조건 덮어써 생략/빈 값이면 NULL로 저장 — email까지 partial로 만들지는 의도 결정 필요 (⚠️ 리뷰 항목 4)
- 내부 서비스(notifier)용 무인증 목록 조회는 `GET /internal/contacts`로 분리 제공 (계약 13) — `/api/contacts`와 응답 스키마 동일

### 검증 단언 (TDD)

- [ ] JWT 없이 `GET /api/contacts` → `401`
- [ ] 잘못된 phone으로 POST → `400`
- [ ] `{"name":"새이름"}`만 담은 PUT → `200`, 응답 `phone`은 기존 값 유지, `email == ""` (email은 partial 아님 — 실측 시맨틱)
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
  "deletedAt": null,
  "alertState": "none | active"
}
```

- PATCH → `200` `{id, alias}` · 없으면 `404`
- DELETE → `204` · 이미 삭제/없음 `404`
- restore → `200` `{id, "status":"restored"}`
- `GET /api/devices/all`을 user가 호출 → `403` `{"error":"admin access required"}`

### 핵심 로직 (불변식)

- device 등록의 원천은 hw-gateway의 `POST /api/devices/seen` (계약 13) — 웹에서는 생성 불가, 수정(alias)/soft delete/복원만
- `alertState`도 `POST /api/devices/seen`(계약 13)이 기록하는 값 — 웹 API로는 수정 불가 (PATCH는 alias만)
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

- restart: hw-gateway `/api/restart`(계약 15) 응답을 상태코드 포함 그대로 중계 · hw-gateway 통신 실패 `502` · 미등록/삭제된 device `400`
- test-alert: hw-gateway `/api/test-alert`(계약 15)에 `{siteId:"test", deviceId:"TEST-DEVICE"}` 전달, 응답 중계

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

- 요청/응답 바디는 recording 서비스 원본을 그대로 통과 (스키마 SSOT는 `docs/spec/recording.md` §HTTP API — 본 계약 범위 밖)
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

- 만료 24시간 고정. temp JWT는 `role` 클레임 없이 링크 id 클레임으로 식별되는 별도 토큰 종류 — 서버가 temp 전용 파서로 인식해 role `temp`(read-only)를 부여한다. **WS 접면(계약 14)도 temp 종류를 식별해 role `temp`를 부여한다.** `/api/*` 인증 미들웨어 접면의 temp 식별·회수 차단은 별도 추적(web-backend 스펙 ⚠1)
- `url`의 호스트: system_settings `site_url` 우선, 없으면 `FRONTEND_URL` env
- 회수(DELETE)는 **블랙리스트 방식** — JWT 서명 자체는 유효하지만 서버가 거부. 블랙리스트가 강제되는 접면: `GET /api/links/verify/{token}`(회수 후 `401`), 링크 목록 제외, **그리고 WS 접속(계약 14) — 접속 시점 차단 + 수립된 연결의 주기적 재검증으로 회수 후 능동 종료**
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
| PUT | `/api/settings/{key}` | admin | `{value: string}` (단건) |
| PUT | `/api/settings` | admin | `{ "<key>": "<value>", ... }` (다건 원자 저장) |

### 출력 (계약)

- GET → `200` `[{"key":"site_url","value":"https://...","updatedAt":"..."}]`
- PUT `/api/settings/{key}` (단건) → `200` `{key, value, updatedAt}` · 없는 key `404`
- PUT `/api/settings` (벌크) → `200` `[{key, value, updatedAt}, ...]`(갱신된 전체) · 요청 내 **하나라도** 미지의 key면 `404`(아무 것도 저장 안 됨) · 값 검증 실패면 `400`(아무 것도 저장 안 됨)

### 핵심 로직 (불변식)

- PUT은 **기존 key만** 갱신 — 새 key 생성 불가 (`404`)
- 알려진 key: `site_url` · `health.service_check_interval_sec`(def 30) · `health.service_down_threshold_sec`(def 90) · `health.sensor_alive_threshold_sec`(def 60)
- `site_url` 변경은 임시 링크 URL(계약 9)과 초대 이메일 링크(계약 10)에 즉시 반영
- **벌크 저장은 원자적** — `PUT /api/settings`로 다건을 저장하면 단일 트랜잭션으로 **전부 성공 또는 전부 롤백**된다. 미지의 key/값 검증 실패가 하나라도 있으면 어떤 key도 변경되지 않는다(부분 저장 없음 — 순차 개별 PUT의 중간 실패로 인한 부분 반영을 대체). 단건 `PUT /api/settings/{key}`는 하위호환으로 유지

### 검증 단언 (TDD)

- [ ] `PUT /api/settings/nonexistent_key` → `404`
- [ ] `PUT /api/settings/site_url` `{"value":"https://x.example"}` → `200` · 이후 temp link 생성 시 `url`이 `https://x.example/view/...`
- [ ] user 토큰으로 GET → `403`
- [ ] `PUT /api/settings`에 유효 `health.*` key 2개 + 미지의 key 1개 → `404`, 이후 `GET /api/settings`에서 그 2개 값도 **변경 전 그대로**(부분 저장 0)
- [ ] `PUT /api/settings`에 `health.*` 임계 3건을 모두 유효값으로 → `200`, 세 값 전부 반영

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
- **unhealthy 전이의 능동 통지**: 감시 대상(서비스/센서)이 `healthy→unhealthy`로 전이하면 `health_events` 기록에 더해 admin에게 능동 push된다 — 접속 중 admin WS 클라이언트에 `system_alarm`(계약 14) 브로드캐스트. `unhealthy→healthy` 복귀도 동일. 통지 실패는 감시 루프를 막지 않음(best-effort). 폴링형 `GET /api/health`만으로는 관제 인프라 자체의 무응답이 조용히 방치될 수 있으므로 전이는 push로 표면화되어야 한다. (notifier 외부 팬아웃·자동 컨테이너 재시작은 인프라 경계로 결정 — 앱 계약에서 제외, 리뷰 항목 5 해소)

### 검증 단언 (TDD)

- [ ] `GET /api/health` 응답의 모든 항목이 `kind ∈ {service, sensor}`, `status ∈ {healthy, unhealthy}`
  ```bash
  curl -s -H "Authorization: Bearer $T" http://localhost:8080/api/health \
    | jq -e 'all(.[]; (.kind=="service" or .kind=="sensor") and (.status=="healthy" or .status=="unhealthy"))'
  ```
- [ ] 응답에 `id == "web-backend"`인 service 항목 없음
- [ ] `entity_kind=sensor` 필터 → 모든 `entityKind == "sensor"`
- [ ] 임의 서비스 컨테이너 중지 → threshold 경과 후 해당 항목 `unhealthy` + events에 전이 1행 추가 · 재시작 → healthy 전이 1행 추가 (전이당 정확히 1행)
- [ ] admin WS 접속 상태에서 서비스 컨테이너 중지 → threshold 경과 후 그 admin WS가 `type=system_alarm` 메시지 1건 수신(unhealthy 전이 표면화); user(비-admin) WS는 미수신

---

## 계약 13: Internal — 무인증, Docker 네트워크 한정

외부 클라이언트 호출 금지가 의도이며, 보호는 Docker 네트워크 격리에 의존한다.
리버스 프록시 레벨 차단은 현재 스택에 구성되어 있지 않음 — ⚠️ 리뷰 항목 3 참조.

### 입력

| Method | Path | 호출자 | 바디 |
|--------|------|--------|------|
| GET | `/healthz` | Docker healthcheck | — |
| GET | `/api/healthz` | (JWT) 진단용 | — |
| GET | `/internal/contacts` | notifier | — |
| GET | `/internal/cameras` | cctv-adapter · youtube-adapter · notifier · recording | — |
| GET | `/internal/settings/{key}` | notifier 등 타 서비스 | — |
| POST | `/api/incidents` | hw-gateway | `{siteId*, deviceId?, description, occurredAt?, isTest?, alertId?}` |
| POST | `/api/devices/seen` | hw-gateway | `{siteId*, deviceId*, alertState?: "none"\|"active"}` |
| POST | `/api/incidents/{id}/resolve-from-sensor` | hw-gateway | 아래 참조 |
| POST | `/internal/alarms` | notifier | `{type?, message?, details?}` |

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

- `GET /healthz` → `200` `{"status":"ok","service":"web-backend"}` — 바디는 JSON 텍스트이나 `Content-Type`은 `text/plain; charset=utf-8` (공통 검증 단언 참조)
- `GET /internal/contacts` → `200` contact 객체 배열 — 계약 5와 동일 스키마 (무인증)
- `GET /internal/cameras` → `200` `[{id, name, location, zone, streamKey, sourceType, sourceUrl, enabled}]` — DB 원본만 반환. `hlsUrl`·`status` 필드는 항상 빈 문자열 (streaming 병합 없음 — 계약 3과 다름)
- `GET /internal/settings/{key}` → `200` `{"key":"<key>","value":"<value>"}` — 없는 key도 `200` + `value: ""` (`404` 아님)
- `POST /api/incidents` → 신규 생성 `201` `{id, siteId, description, occurredAt}` · 동일 `alertId` 재전송 시 **`200` + 기존 incident 반환** (신규 생성 없음) · `siteId` 누락 `400`
- `POST /api/devices/seen` → `200` `{"status":"ok"}`
- `resolve-from-sensor` → `200` `{"status":"resolved","incidentId",  "resolvedByKind":"sensor_button","resolvedById","resolvedByLabel"}` · 매칭 미해결 incident 없음 `404` · **명시적(non-zero) id가 가리키는 incident가** 이미 resolved면 `409`(중복 버튼 방어) · `siteId` 누락 `400`
- `POST /internal/alarms` → `200` `{"status":"ok"}`. `type` 생략 시 `"system_alarm"`으로 기본값 주입. 부작용: admin WS 클라이언트에 `system_alarm` 브로드캐스트(계약 14). DB 영속 없음 — 보장 동작은 "수신 + admin 브로드캐스트"

### 핵심 로직 (불변식)

- `POST /api/incidents` **멱등성(alertId dedup)**: `alertId`가 오면 `incidents.alert_id`의 UNIQUE 부분 인덱스(`alert_id IS NOT NULL` 한정) 기준으로 dedup — 동일 alertId 재전송은 기존 incident를 `200`으로 반환하고 새 행을 만들지 않음. `alertId` 없으면 dedup 없음 (재전송마다 새 incident). 이 dedup은 **동시 요청 하에서도 원자적**이다 — 같은 alertId N건이 동시에 도착해도 정확히 1건만 `201`(신규), 나머지 전부 `200`(동일 incident)이며, UNIQUE 충돌로 인한 `5xx`/`SQLITE_BUSY`나 중복 행·유실이 발생하지 않는다
- `POST /api/incidents`의 `deviceId`는 best-effort device UPSERT 트리거 — `/api/devices/seen`과 동일 의미론(`last_seen` 갱신 + soft-delete 자동 복원). UPSERT 실패가 `201`을 막지 않음
- **호출자 노트**: hw-gateway는 crisis forward 시 `alertId`를 전송한다 — DB dedup 경로가 운영에서 실사용된다. hw-gateway는 forward가 전송 계층 오류 또는 HTTP 5xx면 재시도하고, 2xx 응답을 받은 후에만 자신의 in-memory dedup을 등록하므로, 5xx로 유실된 이벤트는 펌웨어 재전송으로 복구된다(계약 15 / `docs/services/hw-gateway.md` 참조)
- `POST /api/incidents` 신규 생성(`201`) 성공 부작용: 전체 WS 클라이언트에 `crisis_alert` 브로드캐스트 — dedup `200` 경로에서는 브로드캐스트 없음
- `POST /api/devices/seen`: `(site_id, device_id)` UPSERT — 없으면 INSERT(first_seen=last_seen=now), 있으면 `last_seen=now` + `deleted_at=NULL`(soft-delete 자동 복원). `alertState`는 전송값으로 갱신되며 미전송/빈 값이면 `"none"` 처리 — 계약 6 Device 객체의 `alertState`로 노출됨. **멱등**
- `resolve-from-sensor` incident 매칭 폴백 체인: path `{id}`(0 허용) → body `incidentId` → 둘 다 0이면 `siteId`의 가장 최근 **미해결** incident 자동 매칭
- 폴백(둘 다 0) 경로는 미해결 한정 조회이므로 재전송이 `409`에 도달하지 않음: 남은 미해결 incident가 없으면 `404`, 다른 미해결이 있으면 그것을 해소하며 `200` — `409`는 명시적 non-zero id로 이미 resolved인 incident를 지정할 때만 발생
- `kind == "web"` echo는 hw-gateway 측에서 차단되어 이 엔드포인트에 도달하지 않는 것이 시스템 전제
- 성공 부작용: attribution 기록 + WS `incident_resolved` 브로드캐스트 + 아카이브 finalize 비동기 트리거

### 검증 단언 (TDD)

- [ ] `GET /healthz` (무인증) → `200 {"status":"ok","service":"web-backend"}`
- [ ] `GET /internal/contacts` (무인증) → `200` JSON 배열 (계약 5 스키마)
- [ ] `GET /internal/cameras` (무인증) → `200` JSON 배열, 각 항목에 `streamKey`·`sourceType`·`sourceUrl` 포함
- [ ] `GET /internal/settings/site_url` (무인증) → `200` `{"key":"site_url","value":...}` · 없는 key → `200` + `value == ""`
  ```bash
  curl -s http://localhost:8080/internal/settings/no_such_key | jq -e '.value == ""'
  ```
- [ ] `POST /api/devices/seen` 동일 바디 2회 → 둘 다 `200`, device 행은 1개 (멱등)
- [ ] `POST /api/devices/seen` `alertState: "active"` 전송 → 이후 `GET /api/devices`(계약 6)에서 해당 device의 `alertState == "active"`
- [ ] `POST /api/incidents` (siteId 있음) → `201` + 접속 중 WS 클라이언트에 `crisis_alert` 도착
- [ ] `POST /api/incidents` 동일 `alertId` 2회 전송 → 1회차 `201`, 2회차 `200` + 동일 `id` (incidents 행 1개)
  ```bash
  A=$(curl -s -X POST http://localhost:8080/api/incidents -d '{"siteId":"site1","description":"t","alertId":"dup-test-1"}')
  B=$(curl -s -X POST http://localhost:8080/api/incidents -d '{"siteId":"site1","description":"t","alertId":"dup-test-1"}')
  [ "$(echo "$A" | jq .id)" = "$(echo "$B" | jq .id)" ]   # → true
  ```
- [ ] `POST /api/incidents` 동일 `alertId`로 **동시** N(≥12)건 전송 → 정확히 1건 `201`, 나머지 전부 `200`이며 응답 `id` 전부 동일; incidents 행 1개, 응답 중 `5xx`/`SQLITE_BUSY` 0건
  ```bash
  for i in $(seq 12); do curl -s -o /dev/null -w '%{http_code}\n' -X POST \
    http://localhost:8080/api/incidents \
    -d '{"siteId":"site1","description":"t","alertId":"race-1"}' & done; wait
  # → 201 정확히 1개, 나머지 200; 500/503 0개
  ```
- [ ] `POST /api/incidents` 서로 다른 요청(상이 alertId 또는 alertId 없음) **동시** N(≥30)건 → 전부 `201`, `SQLITE_BUSY`/`5xx`/유실 0건, incidents 행 정확히 N 증가
- [ ] `POST /api/incidents`에 `deviceId` 포함 → 이후 `GET /api/devices`에 해당 device 등장 (UPSERT 확인)
- [ ] `POST /api/incidents/0/resolve-from-sensor` + body `incidentId: 0` + 유효 `siteId` → 해당 site 최신 미해결 incident가 resolved, `resolvedByKind == "sensor_button"`
- [ ] 위에서 resolved된 incident의 **명시적 id**로 `POST /api/incidents/{id}/resolve-from-sensor` 재전송 → `409`
- [ ] 폴백(0/0) 요청을 해당 site에 미해결 incident가 없는 상태에서 재전송 → `404` `{"error":"no unresolved incident found for site"}` (`409` 아님)

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
| `system_alarm` | admin 전용 | `{type, message, details}` — `POST /internal/alarms`(계약 13) 수신 시, **또는 HealthMonitor의 healthy↔unhealthy 전이 시**(계약 12) 발생 |

### 핵심 로직 (불변식)

- 인증 실패(토큰 없음/서명 불일치/만료) 시 업그레이드 자체를 `401`로 거절. **회수(blacklist)된 temp 토큰의 업그레이드도 `401`로 거절된다** — WS 접면은 temp 토큰을 temp 종류로 식별해 접속 시점에 blacklist를 확인한다
- **접속 후 주기적 재검증(회수·만료·자격변경 반영)**: 수립된 WS 연결은 최소 `N`초(계약 상한 `N ≤ 60`)마다 토큰 유효성을 재평가한다. (a) temp 토큰이 그 사이 폐기(`DELETE /api/links/{id}`)되었거나, (b) 토큰이 만료(`exp` 경과)되었거나, (c) 소유 사용자의 비밀번호 변경으로 자격증명 경계(계약 1)를 넘긴 경우 중 하나라도 성립하면 서버가 해당 연결을 능동 종료한다. → 폐기된 뷰어의 WS가 토큰 만료(최대 24h)까지 `crisis_alert`/`incident_resolved`를 계속 수신하는 일이 발생하지 않는다
- 서버가 30초마다 ping — 클라이언트는 40초 내 pong 없으면 연결 종료
- role은 JWT의 `role` 클레임에서 추출 (`admin`/`user`). temp link 토큰은 role 클레임이 없으나 WS 접면이 temp 종류로 식별해 role `"temp"`(read-only)를 부여한다 — `system_alarm`은 admin에게만 전달되고 temp/user에는 전달되지 않는다
- `crisis_alert`는 `POST /api/incidents`(계약 13) 성공 시, `incident_resolved`는 웹 resolve(계약 2) 또는 센서 resolve(계약 13) 성공 시 발생. `system_alarm`은 (i) `POST /internal/alarms`(계약 13, notifier가 모든 외부 채널 실패 시 호출) 수신 시, 또는 (ii) 감시 대상(서비스/센서)의 healthy↔unhealthy 상태 전이 시(계약 12) admin에게만 발생

### 검증 단언 (TDD)

- [ ] 토큰 없이 `/ws` 업그레이드 시도 → `401` (연결 수립 안 됨)
- [ ] 유효 JWT로 접속 → 첫 메시지 `type == "connected"`, `payload.role`이 토큰 role과 일치
- [ ] temp link 토큰으로 접속 → 연결 성립, 첫 메시지 `type == "connected"`, `payload.role == "temp"`
- [ ] 회수(`DELETE /api/links/{id}`)된 temp 토큰으로 `/ws` 업그레이드 시도 → `401` (접속 시점 차단)
- [ ] temp 토큰으로 접속 성립 후 admin이 그 링크를 `DELETE` → `N`초(≤60) + 여유 내에 서버가 연결을 능동 종료(close 프레임/연결 끊김 관측), 이후 `crisis_alert` 미수신
- [ ] 로그인 JWT로 접속 성립 후 소유 사용자가 비밀번호 변경 → `N`초(≤60) + 여유 내에 서버가 연결을 능동 종료
- [ ] `POST /api/incidents` → 접속 중인 admin/user/temp 클라이언트 **모두** `crisis_alert` 수신
- [ ] user 클라이언트는 `system_alarm`을 수신하지 않음
- [ ] pong 미응답 클라이언트가 약 40초 후 서버에 의해 연결 종료

---

## 계약 15: hw-gateway inbound HTTP — web-backend ↔ hw-gateway 내부 접면

hw-gateway가 `:8080`으로 노출하는 무인증 HTTP 접면. Docker 네트워크 격리가 전제 (계약 13과 동일 — 외부 노출 금지).
MQTT 발행 페이로드 스키마의 SSOT는 `docs/spec/interface-mqtt.md` — 본 계약은 HTTP 요청/응답만 규정한다.

### 입력

| Method | Path | 호출자 | 바디 |
|--------|------|--------|------|
| GET | `/healthz` | web-backend 헬스 폴링 (계약 12) | — |
| POST | `/api/restart` | web-backend (계약 7 프록시) | `{siteId*, deviceId*, requestedBy?, reason?}` |
| POST | `/api/test-alert` | web-backend (계약 7 프록시) | `{siteId?, deviceId?}` — 빈 값이면 각각 `"test"` / `"TEST-DEVICE"`로 기본값 처리 |
| POST | `/api/alert/resolved` | web-backend (계약 2 resolve 부작용) | `{incidentId, siteId*, resolvedAt?, resolvedBy?: {kind, id, label}, originalAlert?}` |
| GET | `/api/equipment/status` | (현재 코드 내 호출자 없음 — 실측) | — |

### 출력 (계약)

- `GET /healthz` → `200` `{"status":"ok","service":"hw-gateway"}` — 바디는 JSON 텍스트이나 `Content-Type`은 `text/plain; charset=utf-8` (공통 검증 단언 참조)
- MQTT 발행 3종(`restart`/`test-alert`/`alert/resolved`) 공통: 성공 `200` `{"status":"sent","topic":"<발행 토픽>"}` · JSON 파싱 실패/필수 필드 누락 `400` `{"error": ...}` · **최초 브로커 연결 미성립(기동 후 한 번도 연결된 적 없음) `503`** `{"error":"MQTT broker not connected"}` · 발행 실패 `500`
  - 연결이 한 번 성립한 뒤 브로커가 단절된 상태(자동 재연결 진행 중)에서는 `503`이 **아니다** — 발행이 클라이언트 내부 큐에 적재되고 재연결·전송 완료까지 HTTP 응답이 블로킹된다(서버 측 응답 타임아웃 없음). 상세는 `docs/spec/hw-gateway.md`의 "MQTT 발행 API 공통 응답 계약" 참조
  - restart 토픽: `safety/{siteId}/cmd/restart` · test-alert: `safety/{siteId}/alert` · alert/resolved: `safety/{siteId}/alert/resolved`
- `GET /api/equipment/status` → `200` `[{deviceId, siteId, alive, lastHeartbeat, alertState: "none"|"active"}]` (인메모리 상태 — 재시작 시 초기화)

### 핵심 로직 (불변식)

- 전 엔드포인트 무인증 — 네트워크 격리가 유일한 보호막
- `restart`: `siteId`·`deviceId` 필수(`400`), MQTT QoS 1 발행. `requestedBy`는 web-backend가 `"user:{id}"` 형식으로 주입 (계약 7)
- `test-alert`: 실제 알람과 동일 토픽·QoS 2로 발행 — 이후 파이프라인(incident 생성, WS 브로드캐스트)이 실제 알람과 동일하게 동작하는 것이 의도. 페이로드는 `type: "test"`, `test: true` 고정
- `alert/resolved`: `siteId` 필수(`400`) · `resolvedAt` 없으면 서버가 now(RFC3339)로 채움 · `resolvedBy.kind` 없으면 `"web"` 기본값 · QoS 1, retain false. hw-gateway 자신이 이 토픽을 구독하지만 `kind == "web"` echo는 무시 (계약 13 전제와 짝)
- 브로커 연결 상태별 발행 응답은 `docs/spec/hw-gateway.md` "MQTT 발행 API 공통 응답 계약"과 동일 계약: **최초 연결 미성립 시에만** `503`, 연결 성립 후 단절 시에는 재연결까지 블로킹(부분 성공 없음). web-backend는 `503`을 그대로 중계하며(계약 7), 블로킹이 web-backend HTTP 클라이언트 타임아웃(restart 10s)을 초과하면 웹 계층에서는 `502`로 표면화된다

### 검증 단언 (TDD)

- [ ] `GET /healthz` → `200` `{"status":"ok","service":"hw-gateway"}`
  ```bash
  docker compose exec web-backend wget -qO- http://hw-gateway:8080/healthz
  # → {"status":"ok","service":"hw-gateway"}
  ```
- [ ] `POST /api/restart` `deviceId` 누락 → `400` `{"error":"siteId and deviceId are required"}`
- [ ] 브로커 정상 시 `POST /api/test-alert` `{}` → `200` `{"status":"sent","topic":"safety/test/alert"}`
- [ ] `POST /api/alert/resolved` `siteId` 누락 → `400`
- [ ] mosquitto 정지 상태에서 hw-gateway를 (재)기동한 직후 — 최초 브로커 연결 미성립 — `POST /api/restart`(유효 바디) → `503` `{"error":"MQTT broker not connected"}`
- [ ] 브로커 연결이 한 번 성립한 뒤 mosquitto 중지 → `curl --max-time 5 -X POST http://hw-gateway:8080/api/restart`(유효 바디)가 5초 내 미응답 (curl exit 28 — `503` 아님, hw-gateway.md 단언 O2와 교차)
- [ ] `GET /api/equipment/status` → `200` JSON 배열, 모든 항목 `alertState ∈ {none, active}`

---

## ⚠️ 리뷰 필요 (문서-코드 불일치)

실제 라우트/핸들러 대조에서 발견된 어긋남. 본 스펙 본문에는 반영하지 않고 여기에만 기록한다.

1. **~~notifier의 시스템 알람 최후 보루 수신측 부재~~ (해소됨)** — 무인증 internal 라우트 `POST /internal/alarms`가 신설되어 notifier가 이 경로로 호출하고, web-backend가 수신해 WS `system_alarm`(계약 14)을 admin에게 브로드캐스트한다. 계약 13(입력/출력)과 계약 14(WS 발생 경로)에 반영됨. DB 영속은 없으며 보장 동작은 "수신 + admin 브로드캐스트"까지. (번호는 항목 2~4의 교차참조 안정성을 위해 유지)
2. **~~temp link JWT가 WS에서 temp role 식별·회수 차단·만료 재검증을 우회~~ (WS 접면 해소됨 — 계약 14, 이슈 #82)** — WS 접면은 이제 접속 시점에 temp 토큰을 temp 종류로 식별해 role `temp`를 부여하고 blacklist를 확인하며(회수 토큰 업그레이드 `401`), 수립된 연결은 주기적(≤60초) 재검증(회수·`exp` 만료·비밀번호 변경 경계, 계약 1)으로 무효 토큰 연결을 능동 종료한다. 관련 단언("WS `payload.role == "temp"`"·"회수 토큰 WS 접속 `401`"·"회수/비번변경 후 능동 종료")을 계약 14에 추가했다. **남은 사안(미해소)**: `/api/*` 인증 미들웨어 접면에서 temp 토큰이 일반 JWT 파서를 먼저 통과해 blacklist 확인 없이 `role=""`로 통과하는 문제는 web-backend 스펙 ⚠️1(authz 범위)로 별도 추적한다 — WS 재검증 계약과는 접면이 다르다.
3. **무인증 internal 엔드포인트가 리버스 프록시를 그대로 통과해 외부 노출됨 (알려진 시스템 갭)** — 현 스택의 유일한 리버스 프록시인 web-frontend nginx는 `/api/` 전체를 web-backend로 무차별 프록시하고, docker-compose가 web-frontend:80을 호스트 `3080`으로 노출한다. 따라서 외부에서 무인증 internal 엔드포인트(계약 13의 `POST /api/incidents`, `POST /api/devices/seen`, `POST /api/incidents/{id}/resolve-from-sensor`와 계약 9의 internal 폴백 `POST /api/links/temp`)가 인증 없이 그대로 호출된다(200/201). 차단 구성이 repo 내 어디에도 없다. → 의도 결정 필요: (a) 리버스 프록시에서 해당 경로 차단(location 규칙), (b) internal 엔드포인트를 `/internal/*` 프리픽스로 이동 + 프록시 미노출, 또는 (c) 내부 호출 인증 도입. 결정 전까지 "외부에서 internal 엔드포인트 `4xx`" 배포 게이트 단언은 계약에 두지 않는다.
4. **`PUT /api/contacts/{id}`의 email만 partial 시맨틱에서 벗어남 (실측 — web-backend 스펙 ⚠️5와 동일 사안)** — name/phone은 빈 값 무시, notifyEmail은 생략 시 유지인데 email은 요청값으로 무조건 덮어써서, email을 생략한 partial PUT이 기존 email을 NULL로 삭제한다. 본문(계약 5)은 이 실측 동작을 계약으로 기술했다. → 의도 결정 필요: (a) email도 생략 시 유지(partial 통일 — email 삭제는 별도 신호 필요), 또는 (b) 현 덮어쓰기 동작 유지. (a)로 결정되면 계약 5 입력 표·불변식·단언을 함께 갱신해야 한다.
5. **[해소 — 설계자 결정] unhealthy 자동 복구 범위.** unhealthy 상태 전이의 admin 통지(계약 12 → 계약 14 `system_alarm`)까지만 앱이 계약한다. hang 상태 서비스의 자동 컨테이너 재시작은 인프라 계층(docker compose restart policy / healthcheck 연동) 책임으로 두어 앱 계약에서 제외하며, unhealthy 전이의 notifier 외부 채널 팬아웃도 채택하지 않는다.
