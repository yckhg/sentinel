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
- 에러 코드 매핑: `400` 잘못된 입력 · `401` 인증 실패/만료 · `403` 권한 부족 · `404` 없음 · `409` 상태 충돌 · `429` 레이트 리밋(login/register 및 테스트 발송 `(channel,target)` 분당 1건) · `502` 하위 서비스 통신 실패(recording · hw-gateway · notifier)
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
- JWT 유효기간 24h, HS256. 토큰 페이로드는 `userId`·`role`(`admin`/`user`)·`iat`(발급 시각, Unix epoch 초) 클레임을 담는다. `iat`는 자격증명 변경 경계 판정의 기준값이다(아래 불변식)
- `/auth/pending|approve|reject|users`는 인증 미들웨어 밖 — 핸들러가 직접 admin JWT 검증 (프리픽스 `/api/` 아님이 계약)
- 레이트 리밋은 IP 단위, `/auth/login` 10/min, `/auth/register` 5/min — 다른 엔드포인트에는 적용하지 않음
- **비밀번호 변경은 기존 발급 토큰을 무효화 (자격증명 변경 경계)** — 서버는 각 사용자의 경계를 **DB에 영속**한다(`users.password_changed_at`, credential_boundary). `POST /api/auth/change-password` 성공 시 이 값이 변경 시각으로 갱신되고, 인증 검증은 제시된 토큰의 `iat`가 소유 사용자의 `password_changed_at`보다 **이르면 거부(`401`)**한다. 따라서 변경 이전에 발급된 모든 로그인 JWT(변경에 사용한 토큰 자신 포함)가 즉시 `401`이 되어 클라이언트는 재로그인해야 하며, 경계 이후 재로그인으로 얻은(=`iat`가 경계 이상인) 토큰만 유효하다. 경계가 **DB 영속**이므로 컨테이너 재시작 후에도 변경-이전 토큰은 부활하지 않고 계속 `401`이다(in-memory 경계였다면 재시작 시 탈취 토큰이 만료까지 부활). 비밀번호 미변경 사용자의 토큰은 만료(24h)까지 유효. (경계 데이터(`iat` 클레임 + `password_changed_at` 영속)와 비교 시맨틱은 계약이며, 이를 per-user token-version으로 등가 구현하는 것은 허용된다.)

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
- [ ] 로그인 토큰 `t`로 `GET /api/incidents` → `200`; `POST /api/auth/change-password` 성공 후 **같은 `t`**로 `GET /api/incidents` → `401`; 변경 후 재로그인으로 얻은 새 토큰 → `200`; 변경 후 **컨테이너 재시작**해도 `t`는 여전히 `401`(경계 DB 영속 — 재시작으로 부활하지 않음); 다른 사용자 V가 변경 전 발급받은 토큰은 영향받지 않아 여전히 `200`

---

## 계약 2: Incidents (`/api/incidents*`)

### 입력

| Method | Path | Auth | 입력 |
|--------|------|------|------|
| GET | `/api/incidents` | user | query: `page`(≥1, def 1) `limit`(def 20, max 100) `from` `to`(occurred_at, SQLite datetime) `status`(`open\|resolved`) |
| GET | `/api/incidents/active` | user | — (미해결 사고 배너 backfill 전용) |
| PATCH | `/api/incidents/{id}/resolve` | admin | `{resolutionNotes?: string}` — **선택**(생략·빈 문자열·공백 허용) |
| POST | `/api/incidents` | **none (internal)** | → 계약 13 |

### 출력 (계약)

`GET /api/incidents` → `200`:

```json
{
  "data": [{
    "id": 12, "siteId": "site-001", "description": "gas leak",
    "occurredAt": "2026-04-13 10:20:30",
    "confirmedAt": null, "confirmedBy": null, "isTest": false,
    "status": "open | resolved",
    "resolvedAt": null, "resolvedBy": null, "resolutionNotes": null,
    "resolvedByKind": null, "resolvedById": null, "resolvedByLabel": null
  }],
  "pagination": { "page": 1, "limit": 20, "total": 57 }
}
```

`GET /api/incidents/active` → `200` — **미해결(open) 사고** 배열. 각 원소는 WS `crisis_alert` payload(계약 14)와 **동형**이다 — 배너 backfill이 실시간 push와 동일한 모양을 재구성하도록 계약된다(반쪽 배너 방지):

```json
[{
  "incidentId": 12,
  "siteId": "site-001",
  "description": "gas leak",
  "occurredAt": "2026-04-13 10:20:30",
  "isTest": false,
  "status": "open",
  "site": { "address": "...", "managerName": "...", "managerPhone": "010-1234-5678" }
}]
```

- `PATCH .../resolve` → `200` `{"status":"resolved", "resolvedByKind":"web", "resolvedById":"<username>", "resolvedByLabel":"..."}` · 이미 resolved면 `409` · `resolutionNotes`는 **선택**(생략·빈 문자열·공백뿐이어도 `400`이 아니라 `200`으로 해소되며 빈 값/`null`로 저장)

### 핵심 로직 (불변식)

- 상태 기계: `open → resolved` (중간 확인 상태 없음 — `acknowledged` 상태·확인 액션은 계약에서 제거됨; resolved는 종단 — 재해결 `409`)
- **양방향 해소 attribution**: `resolvedByKind ∈ {"web", "sensor_button", null}` — 웹 해제는 본 계약, 센서 버튼 해제는 계약 13(`resolve-from-sensor`). 어느 경로든 동일 필드에 기록
- resolve 성공 부작용 3종: (a) recording 아카이브 finalize 비동기 트리거 (b) hw-gateway `/api/alert/resolved`(계약 15) 경유 MQTT `safety/{siteId}/alert/resolved` 발행 (c) WS `incident_resolved` 브로드캐스트 (계약 14)
- `limit > 100` 요청은 100으로 클램프 (에러 아님)
- **`/api/incidents/active` (배너 backfill 계약)**: `status ∈ {open}`만 반환(resolved 제외; `acknowledged`는 존재하지 않음), 발생시각 내림차순. 각 원소의 식별자는 계약 2 목록의 `id`가 아니라 `crisis_alert`와 동일하게 **`incidentId`**이며, 사고 site의 `{address, managerName, managerPhone}`를 **`site`로 중첩** 포함한다(sites 테이블(계약 4)에서 조인). 이 payload 동형성 보장으로 web-frontend가 접속/재접속 시 진행 중 위기 배너를 실시간 `crisis_alert`와 같은 모양으로 재구성하며, 현장 연락 정보가 결손된 반쪽 배너가 되지 않는다

### 검증 단언 (TDD)

- [ ] `GET /api/incidents?limit=500` → `200`, `pagination.limit == 100`
  ```bash
  curl -s -H "Authorization: Bearer $T" 'http://localhost:8080/api/incidents?limit=500' \
    | jq -e '.pagination.limit == 100'
  ```
- [ ] `status=resolved` 필터 → `data[]`의 모든 `status == "resolved"`
- [ ] open incident에 resolve(notes 있음) → `200`, `resolvedByKind == "web"`
- [ ] 같은 incident에 resolve 재호출 → `409`
- [ ] `resolutionNotes: ""`(또는 생략·공백) 로 resolve → `400`이 아니라 `200`(노트 선택 — 빈 값/`null`로 저장)
- [ ] user 토큰으로 resolve → `403` (해소는 admin 전용; 조회 `GET /api/incidents`는 user `200`)
- [ ] resolve 성공 직후 WS 클라이언트가 `incident_resolved` 메시지 수신 (계약 14 단언과 교차)
- [ ] `GET /api/incidents/active` → `200`, 모든 원소 `status == "open"`(resolved·acknowledged 미포함), 각 원소에 `incidentId`·`site.address`·`site.managerName`·`site.managerPhone` 존재
- [ ] `GET /api/incidents/active` 원소의 키 집합이 `crisis_alert`(계약 14) payload와 동형 — `incidentId, siteId, description, occurredAt, isTest, site.{address,managerName,managerPhone}`
  ```bash
  curl -s -H "Authorization: Bearer $T" http://localhost:8080/api/incidents/active \
    | jq -e 'all(.[]; has("incidentId") and .status=="open" and (.site|has("address") and has("managerName") and has("managerPhone")))'
  ```

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
| POST | `/api/devices` | admin | `{siteId*, deviceId*, alias?}` — 명시 등록 또는 재활성(생성-또는-재활성) |
| GET | `/api/devices` | user | — (삭제 제외 목록) · query: `siteId`+`deviceId`(합성키→수치 id 사상 필터) |
| GET | `/api/devices/all` | admin | — (soft-deleted 포함) |
| GET | `/api/devices/{id}` | user | — (수치 DB id 단건 조회, 장비 현재-상태 검색) |
| PATCH | `/api/devices/{id}` | admin | `{alias: string}` (alias 수정 — admin 전용) |
| DELETE | `/api/devices/{id}` | admin | — (soft delete, sticky — admin 전용) |

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

- `id`는 서버가 발급하는 **surrogate 수치 PK**이며 경로 파라미터 `{id}`가 이를 가리킨다(합성키 `siteId:deviceId`와 구분). `lastSeen`은 **`string | null`** — 명시 등록됐으나 최초 heartbeat 미수신인 오프라인 대기 장치는 `null`이다.
- `POST /api/devices` (admin) → 신규 등록 `201` device object · 동일 `(siteId,deviceId)`가 이미 미삭제로 존재 `409` `{"error":"device already registered"}` · soft-deleted였다면 **재활성** `200` device object(`deletedAt`이 `null`로 복귀, `reappear_alerted_at` 리셋, **`lastSeen`은 재활성으로 갱신되지 않음** — 기존값 유지, `alias`는 전송 시에만 갱신). 웹에서 장치를 명시 등록/재활성하는 유일 경로다(sticky 삭제의 유일한 복원 경로)
- `GET /api/devices/{id}` → `200` device object (미삭제) · 미등록/삭제(`deletedAt`) → `404` · 검색은 합성키 `siteId:deviceId`를 `?siteId=&deviceId=` 필터로 수치 id에 사상 후 단건 조회(spec system-status-aggregate 단언 D)
- PATCH → `200` `{id, alias}` · 없으면 `404`
- DELETE → `204` · 이미 삭제/없음 `404`
- `GET /api/devices/all`을 user가 호출 → `403` `{"error":"admin access required"}`

### 핵심 로직 (불변식)

- device 등록 원천은 둘이다 — (a) hw-gateway의 `POST /api/devices/seen`(계약 13) 자동발견, (b) admin의 `POST /api/devices` **명시 등록/재활성**. 웹에서 명시 등록·재활성이 가능하며(기존 "웹 생성 불가"에서 정정), 관리 변이(등록·삭제·alias PATCH)는 admin 전용
- `alertState`도 `POST /api/devices/seen`(계약 13)이 기록하는 값 — 웹 API로는 수정 불가 (PATCH는 alias만)
- **sticky 삭제 + 재출현 경보**: 삭제는 soft delete(`deletedAt`)이며, 삭제 후 device의 heartbeat/alert(`seen`·`incidents`)가 다시 와도 **자동 복원되지 않는다** — 존재감(`lastSeen`)만 갱신되고 `deletedAt`은 non-null로 유지된다. 삭제 장치가 재신호하면 대신 WS `device_reappeared`(계약 14)가 삭제→재출현 사이클당 **정확히 1회** 발행된다. 복원(재활성)의 유일 경로는 admin의 `POST /api/devices`(200)다
- **존재감(`lastSeen`)과 생명주기(`deletedAt`)는 직교**한다 — 삭제 여부와 무관하게 관측 보고는 `lastSeen`을 갱신하며, 명시 등록된 미관측 장치는 `lastSeen == null`(오프라인 대기)
- `GET /api/devices`는 `deletedAt IS NULL`만 반환

### 검증 단언 (TDD)

- [ ] user 토큰으로 `GET /api/devices/all` → `403`; user 토큰으로 `POST /api/devices`·`DELETE /api/devices/{id}`·`PATCH /api/devices/{id}` → `403`(관리 변이는 admin 전용)
- [ ] admin `POST /api/devices` `{siteId,deviceId}` (신규) → `201`; 동일 바디 재호출(미삭제 존재) → `409`; 그 device를 DELETE 후 다시 `POST /api/devices` → `200`(`deletedAt == null` 재활성, `lastSeen` 불변)
- [ ] DELETE 후 `GET /api/devices`에 미포함, `GET /api/devices/all`(admin)에는 `deletedAt` 채워져 포함
- [ ] **sticky**: soft-deleted device에 `POST /api/devices/seen` (계약 13) → 이후 `GET /api/devices`에 **재등장하지 않음**(삭제 유지, `GET /api/devices/all`에서 `deletedAt` 여전히 non-null, `lastSeen`만 갱신); 대신 삭제→재출현 사이클 최초 seen에서 WS `device_reappeared` 1회 발행
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

- 요청/응답 바디는 recording 서비스 원본을 그대로 통과한다. 응답 **스키마 값의 정의 SSOT는 `docs/spec/recording.md` §출력·§HTTP API**가 소유한다(enum 값 나열을 본 계약에서 재정의하지 않음) — 다만 `GET /api/archives`의 `status` enum을 **소비하는 쪽(web-frontend)의 의무는 본 계약이 규정**한다(아래 핵심 로직 "아카이브 status 소비자 계약").
- HLS 응답(`application/vnd.apple.mpegurl`)과 다운로드 바이너리 스트림은 Content-Type 보존하여 forward
- recording 서비스 통신 실패 → `502`

### 핵심 로직 (불변식)

- 이 그룹에서 web-backend가 보장하는 것은 **인증 게이트 + 투명 프록시** 두 가지뿐 — 바디 변형·필터링 없음
- JWT 없이는 어떤 프록시 경로도 통과 불가
- **아카이브 status 소비자 계약**: `GET /api/archives` 응답 각 항목의 `status`는 recording 스펙([`docs/spec/recording.md`](recording.md) §출력·§HTTP API)이 **정의를 소유**하는 enum 6종 `{protecting, pending, finalizing, processing, completed, failed}` 중 하나다(값 정의 SSOT=recording 스펙, 본 계약은 값을 재정의하지 않음). 이 enum을 소비하는 web-frontend는 다음을 **의무로** 지킨다 — (a) 6종 **전부**를 처리한다, (b) 이 6종에 없는 **미지 상태가 오면 안전 fallback**한다: 미완료(진행 중)로 취급하며 임의로 완료(`completed`)로 표시하지 않는다, (c) `failed`는 오류 종단으로 **사용자에게 노출**한다(실패 표기+사유 `lastError`), (d) `completed` 항목의 `completedAt`(RFC3339 **UTC**)을 **로컬 시각으로 변환해 표시**한다(값의 정본은 UTC, 고아 필드 방지). recording 기동 복구(recording 스펙 단언 P/P-2) 도입으로 재시작 직후 `finalizing`·`processing`·`failed`가 노출될 확률이 증가하므로 이 소비자 의무를 판정 가능한 계약으로 고정한다.
- **아카이브 다운로드 게이트(투명 프록시)**: `GET /api/archives/{id}/download`는 recording의 다운로드 계약을 그대로 통과시킨다 — `completed`만 `video/mp4` 서빙, 그 외 모든 비-`completed`(미완료 4종 **및** `failed`)는 **409**, 아카이브 부재는 **404**. web-backend는 이 응답(상태 코드·헤더·바디)을 변형 없이 forward하며 recording 통신 실패 시에만 `502`를 낸다. `completedAt`/`lastError` 신규 필드도 생산자(recording)가 만들고 본 프록시는 그대로 통과시킨다(투명 프록시는 새 응답 필드를 만들지 않음).

### 검증 단언 (TDD)

- [ ] JWT 없이 `GET /api/storage` → `401` (프록시 이전 차단)
- [ ] recording 컨테이너 중지 후 `GET /api/archives` → `502`
- [ ] `GET /api/recordings/{key}/play` 응답 Content-Type이 recording 서비스 응답과 동일 (m3u8 보존)
- [ ] **아카이브 status enum**: `GET /api/archives` 응답의 모든 원소 `status`가 6종 `{protecting, pending, finalizing, processing, completed, failed}` 중 하나다(recording 스펙이 소유하는 enum과 일치)
  ```bash
  curl -s -H "Authorization: Bearer $T" http://localhost:8080/api/archives \
    | jq -e 'all(.[]; .status | IN("protecting","pending","finalizing","processing","completed","failed"))'
  ```
- [ ] **소비자 fallback 의무(web-frontend 교차)**: 위 6종 밖의 미지 `status`를 받은 web-frontend는 이를 완료로 표시하지 않고 미완료(진행 중)로 안전 처리하며, `failed`는 사용자에게 실패(+사유)로 노출한다 ([`web-frontend.md`](web-frontend.md) §검증 단언 **단언 R**(아카이브 status 소비자 판정)과 교차 판정)

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
- PUT `/api/settings` (벌크) → `200` `[{key, value, updatedAt}, ...]`(갱신된 전체) · 요청 내 **하나라도** 미지의 key거나 값 검증(타입/범위/교차제약, 아래 표) 실패면 **`400`**(아무 것도 저장 안 됨). 벌크 검증 실패는 리소스-부재가 아니라 요청 무효이므로 `400`으로 **통일**한다 — 단건 `PUT /api/settings/{key}`의 미지 key `404`(경로 리소스 부재)와 구분. ⚠ 현행 코드가 벌크 미지 key에 `404`를 반환한다면 계약(`400`)에 맞춰 코드를 고친다

### 핵심 로직 (불변식)

- PUT은 **기존 key만** 갱신 — 새 key 생성 불가 (`404`)
- 알려진 key (타입·유효범위·교차제약 — 무효값 판정 SSOT):

| key | 타입 | 유효 범위 | 교차 제약 | 기본값 |
|-----|------|-----------|-----------|--------|
| `site_url` | string | 비어있지 않은 절대 `http(s)://` URL | — | — |
| `health.service_check_interval_sec` | 정수(초) | 5 ~ 3600 | — | 30 |
| `health.service_down_threshold_sec` | 정수(초) | 5 ~ 86400 | 반영 후 최종 상태 기준 `≥ health.service_check_interval_sec` | 90 |
| `health.sensor_alive_threshold_sec` | 정수(초) | 5 ~ 86400 | — | 60 |

  **무효값** = 위 타입/범위/교차제약 위반 — 비정수 문자열, 하한 미만(예: interval `< 5`), 상한 초과, `site_url` 파싱 실패(비-URL·비 http(s)), 또는 `service_down_threshold_sec < service_check_interval_sec`(교차제약 위반). 무효값·미지 key는 벌크에서 `400`(부분 저장 없음)
- `site_url` 변경은 임시 링크 URL(계약 9)과 초대 이메일 링크(계약 10)에 즉시 반영
- **벌크 저장은 원자적** — `PUT /api/settings`로 다건을 저장하면 단일 트랜잭션으로 **전부 성공 또는 전부 롤백**된다. 미지의 key/값 검증 실패가 하나라도 있으면 어떤 key도 변경되지 않는다(부분 저장 없음 — 순차 개별 PUT의 중간 실패로 인한 부분 반영을 대체). 단건 `PUT /api/settings/{key}`는 하위호환으로 유지

### 검증 단언 (TDD)

- [ ] `PUT /api/settings/nonexistent_key` → `404`
- [ ] `PUT /api/settings/site_url` `{"value":"https://x.example"}` → `200` · 이후 temp link 생성 시 `url`이 `https://x.example/view/...`
- [ ] user 토큰으로 GET → `403`
- [ ] `PUT /api/settings`에 유효 `health.*` key 2개 + 미지의 key 1개 → `400`, 이후 `GET /api/settings`에서 그 2개 값도 **변경 전 그대로**(부분 저장 0)
- [ ] `PUT /api/settings`에 유효 key 1개 + 무효값 1개(예: `health.service_check_interval_sec:"abc"` 또는 `"2"`(<5), 또는 교차제약 위반) → `400`, 부분 저장 0
- [ ] `PUT /api/settings`에 `health.*` 임계 3건을 모두 유효값으로 → `200`, 세 값 전부 반영

---

## 계약 12: Health — 통합 시스템 상태 (`/api/health*`)

### 입력

| Method | Path | Auth | 입력 |
|--------|------|------|------|
| GET | `/api/health` | user | — |
| GET | `/api/health/summary` | user | — (현재-상태 집계 요약 창, spec system-status-aggregate) |
| GET | `/api/health/events` | user | query: `limit`(def 50) `offset`(def 0) `entity_kind`(`service`\|`sensor`) `entity_id`(`siteId:deviceId`, 장비 드릴다운) |

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

`GET /api/health/summary` → `200` 집계 (spec system-status-aggregate — 크기가 예외 장비 수에만 비례):

```json
{
  "summary":  {"healthy": 200, "abnormal": 1, "offline": 1},
  "services": [{"id": "hw-gateway", "status": "healthy | unhealthy"}],
  "exceptions": [{"id": "site1:VOICE-01", "displayName": "음성센서 1", "category": "abnormal | offline", "ageSec": 3600, "reason": "no heartbeat"}],
  "exceptionsOverflow": 0
}
```

- `summary` 세 카운트의 합 = 미삭제 장비 총수. `exceptions`는 abnormal/offline **만** 담고 상한 50건(초과분은 `exceptionsOverflow`가 대표). healthy 장비는 카운트로만 대표(개별 미나열). `services`는 계약 12 감시 집합 전체(항상 완전). 카운트/예외는 단일 읽기 트랜잭션(단일 일관 스냅샷)에서 산출.
- **[델타] `entity_id` 필터**: `GET /api/health/events?entity_id=<siteId:deviceId>`는 그 장비의 sensor 전이(online/offline)만 반환한다(다른 장비 전이 0건 — 센서 `entityId`가 `siteId:deviceId`로 매칭). 장비 이력 드릴다운의 접면이며, 집계 응답에는 어떤 전이-로그도 실리지 않는다.

### 핵심 로직 (불변식)

- 판정: service는 연속 실패가 `health.service_down_threshold_sec` 이상 지속되어야 unhealthy 확정(깜빡임 무시), 성공 1회면 즉시 healthy · sensor는 `now - last_seen > health.sensor_alive_threshold_sec`이면 unhealthy
- sensor `id`는 `siteId:deviceId`, `name`은 alias 우선(없으면 device_id)
- 모니터 제외: web-backend 자기 자신, mosquitto(HTTP healthz 없음)
- events는 **상태 전이 시점에만** 기록 (무변화 미기록 — 테이블 폭증 방지)
- **unhealthy 전이의 능동 통지**: 감시 대상(서비스/센서)이 `healthy→unhealthy`로 전이하면 `health_events` 기록에 더해 admin에게 능동 push된다 — 접속 중 admin WS 클라이언트에 `system_alarm`(계약 14) 브로드캐스트. `unhealthy→healthy` 복귀도 동일. 통지 실패는 감시 루프를 막지 않음(best-effort). 폴링형 `GET /api/health`만으로는 관제 인프라 자체의 무응답이 조용히 방치될 수 있으므로 전이는 push로 표면화되어야 한다. (notifier 외부 팬아웃·자동 컨테이너 재시작은 인프라 경계로 결정 — 앱 계약에서 제외, 리뷰 항목 5 해소)
- **재접속 admin에게 미해소 unhealthy 스냅샷 재전달 (⚠ 권장 채택)**: 위 전이 push는 전이 **순간 접속 중**인 admin에게만 닿으므로, 그때 미접속이던 admin은 통지를 영구 유실한다("조용히 방치 안 됨" 불변식과 상충). 이를 보정하기 위해 admin role WS가 새로 접속하면(계약 14 `connected` 직후) 서버는 현재 `unhealthy` 상태인 모든 감시 대상의 스냅샷을 그 admin에게만 `system_alarm`(계약 14, `details.status=="unhealthy"`)으로 재전달한다. 이로써 재접속 admin이 진행 중 미해소 unhealthy를 즉시 관측한다. (⚠ 현재 상태 스냅샷은 in-memory이므로 web-backend 재시작 직후에는 스냅샷이 비어 재평가로 다시 채워질 때까지 공백일 수 있음 — 이 한계는 감수한다.)

### 검증 단언 (TDD)

- [ ] `GET /api/health` 응답의 모든 항목이 `kind ∈ {service, sensor}`, `status ∈ {healthy, unhealthy}`
  ```bash
  curl -s -H "Authorization: Bearer $T" http://localhost:8080/api/health \
    | jq -e 'all(.[]; (.kind=="service" or .kind=="sensor") and (.status=="healthy" or .status=="unhealthy"))'
  ```
- [ ] 응답에 `id == "web-backend"`인 service 항목 없음
- [ ] `entity_kind=sensor` 필터 → 모든 `entityKind == "sensor"`
- [ ] 임의 서비스 컨테이너 중지 → threshold 경과 후 해당 항목 `unhealthy` + events에 전이 1행 추가 · 재시작 → healthy 전이 1행 추가 (전이당 정확히 1행)
- [ ] admin WS 접속 상태에서 서비스 컨테이너 중지 → threshold 경과 후 그 admin WS가 `type=system_alarm` 메시지 1건 수신(`details.entityKind=="service"`·해당 `entityId`·`details.status=="unhealthy"`); user(비-admin) WS는 미수신
- [ ] **센서 unhealthy 표면화**: admin WS 접속 상태에서 등록된 센서의 heartbeat가 끊겨 `health.sensor_alive_threshold_sec` 경과 → 그 admin WS가 `type=system_alarm`(`details.entityKind=="sensor"`·해당 `entityId`·`details.status=="unhealthy"`) 1건 수신; user WS는 미수신
- [ ] **재접속 스냅샷 재전달(⚠ 권장)**: 어떤 대상이 `unhealthy`인 상태에서 admin WS가 새로 접속 → `connected` 직후 그 대상에 대한 `system_alarm`(`details.status=="unhealthy"`) 스냅샷 수신

---

## 계약 13: Internal — 무인증(일부 앱레벨 토큰), Docker 네트워크 한정

외부 클라이언트 호출 금지가 의도이며, 대부분 경로의 보호는 Docker 네트워크 격리에 의존한다.
단 **`POST /api/devices/seen`·`POST /api/incidents` 두 경로는 앱레벨 `X-Internal-Token` fail-closed 게이트**로 추가 방어된다(아래 핵심 로직). 나머지 internal 경로(`/internal/*`, `resolve-from-sensor`, 계약 9 links/temp 폴백 등)는 여전히 네트워크 격리에만 의존한다.
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

- **internal 토큰 게이트 (seen·incidents fail-closed)**: `POST /api/devices/seen`과 `POST /api/incidents`는 `X-Internal-Token` 헤더를 fail-closed로 검증한다 — 헤더 부재·서버 시크릿(`INTERNAL_TOKEN`)과 불일치·**서버 시크릿 미설정** 중 하나라도면 `401`(constant-time 비교). 유효 시크릿(hw-gateway가 두 호출에 동봉 — 계약 15 / `hw-gateway.md`)일 때만 통과한다. 위기 유입·heartbeat의 오구성 유실은 치명적이므로 부정 케이스를 명시 `401`로 판정한다. 이 두 경로 외 나머지 internal 엔드포인트는 이 게이트를 적용하지 않는다(네트워크 격리 전제 유지)
- `POST /api/incidents` **멱등성(alertId dedup)**: `alertId`가 오면 `incidents.alert_id`의 UNIQUE 부분 인덱스(`alert_id IS NOT NULL` 한정) 기준으로 dedup — 동일 alertId 재전송은 기존 incident를 `200`으로 반환하고 새 행을 만들지 않음. `alertId` 없으면 dedup 없음 (재전송마다 새 incident). 이 dedup은 **동시 요청 하에서도 원자적**이다 — 같은 alertId N건이 동시에 도착해도 정확히 1건만 `201`(신규), 나머지 전부 `200`(동일 incident)이며, UNIQUE 충돌로 인한 `5xx`/`SQLITE_BUSY`나 중복 행·유실이 발생하지 않는다
- `POST /api/incidents`의 `deviceId`는 best-effort device 존재감 UPSERT 트리거 — `/api/devices/seen`과 동일하게 **sticky**(`last_seen` 갱신, `deleted_at` 불변 — 삭제 장치가 위기를 내도 복원되지 않음). 위기 맥락이므로 `alert_state`는 `active`로 기록되며(신규·기존 공통, 이후 heartbeat가 조정), 삭제 장치의 최초 위기 재신호에는 seen과 **공유하는** 재출현 가드로 `device_reappeared`를 1회 발행한다(두 경로가 서로의 경보를 잠식하지 않음). UPSERT 실패가 `201`을 막지 않음
- **호출자 노트**: hw-gateway는 crisis forward 시 `alertId`를 전송한다 — DB dedup 경로가 운영에서 실사용된다. hw-gateway는 forward가 전송 계층 오류 또는 HTTP 5xx면 재시도하고, 2xx 응답을 받은 후에만 자신의 in-memory dedup을 등록하므로, 5xx로 유실된 이벤트는 펌웨어 재전송으로 복구된다(계약 15 / `docs/services/hw-gateway.md` 참조)
- `POST /api/incidents` 신규 생성(`201`) 성공 부작용: 전체 WS 클라이언트에 `crisis_alert` 브로드캐스트 — dedup `200` 경로에서는 브로드캐스트 없음
- `POST /api/devices/seen`: `(site_id, device_id)` **존재감** UPSERT — 없으면 INSERT(`first_seen=last_seen=now`, `alert_state` 기본 `none`), 있으면 `last_seen=now`만 갱신. **`deleted_at`은 건드리지 않는다**(sticky — soft-delete된 장치는 seen이 와도 복원되지 않고 존재감만 갱신됨, 계약 6). 삭제 장치에 seen이 착지하면 rowcount-가드된 dedup UPDATE(`reappear_alerted_at` NULL→now)로 삭제→재출현 사이클당 **정확히 1회** WS `device_reappeared`를 발행한다(정상 heartbeat 핫패스는 추가 쓰기 없음; seen·incidents가 가드를 공유해 상호 잠식 없음). `alertState`는 전송값으로 갱신(COALESCE)되며 미전송/빈 값이면 기존값 유지(신규 행은 `none`) — 계약 6 Device 객체의 `alertState`로 노출됨. **멱등**
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
- [ ] **internal 토큰 fail-closed**: `POST /api/devices/seen`·`POST /api/incidents`에 `X-Internal-Token` 헤더 없이(또는 서버 시크릿과 불일치) 호출 → `401`; 유효 `X-Internal-Token` 동봉 → 통과(2xx). 서버 `INTERNAL_TOKEN` 미설정 상태에서도 두 경로 모두 `401`(fail-closed)
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
| `system_alarm` | admin 전용 | `{type, message, details}` — `POST /internal/alarms`(계약 13) 수신 시, **또는 HealthMonitor의 healthy↔unhealthy 전이/재접속 스냅샷 시**(계약 12) 발생. health-출처 `details` 하위 스키마는 아래 고정 |
| `device_reappeared` | admin 전용 | `{siteId, deviceId, lastSeen: string\|null}` — soft-deleted 장치가 `seen`/`incidents`로 재신호한 삭제→재출현 사이클당 **최초 1회**(계약 6·13, sticky). 장치는 복원되지 않으며 admin이 재활성 여부를 결정한다. `lastSeen`은 명시 등록 후 미관측 삭제 장치면 `null` |

**`system_alarm` payload 세부**

- 공통 봉투: `{type, message, details}`. `message`는 사람 읽기용 텍스트(계약 아님).
- `POST /internal/alarms`(계약 13) 출처: notifier가 보낸 `{type?, message?, details?}`를 그대로 전달(`details` 스키마는 notifier 재량).
- **HealthMonitor 출처(계약 12 — 전이 push 및 재접속 스냅샷)**: `type == "system_alarm"`, `details`는 다음 하위 스키마로 **고정**한다:
  ```json
  { "entityKind": "service | sensor", "entityId": "hw-gateway | site1:VOICE-01", "status": "unhealthy | healthy" }
  ```
  즉 대상의 종류·식별자·(전이 후/스냅샷 시점) 상태를 담는다 — 수신 admin이 "무엇이 어떤 상태인지"를 payload만으로 판정할 수 있어야 한다(계약 12의 O2·센서·스냅샷 단언이 이 필드로 기술된다). `entityId`는 service면 서비스명, sensor면 `siteId:deviceId`(계약 12 `id`와 동일).

### 핵심 로직 (불변식)

- 인증 실패(토큰 없음/서명 불일치/만료) 시 업그레이드 자체를 `401`로 거절. **회수(blacklist)된 temp 토큰의 업그레이드도 `401`로 거절된다** — WS 접면은 temp 토큰을 temp 종류로 식별해 접속 시점에 blacklist를 확인한다
- **접속 후 주기적 재검증(회수·만료·자격변경 반영)**: 수립된 WS 연결은 최소 `N`초(계약 상한 `N ≤ 60`)마다 토큰 유효성을 재평가한다. (a) temp 토큰이 그 사이 폐기(`DELETE /api/links/{id}`)되었거나, (b) 토큰이 만료(`exp` 경과)되었거나, (c) 소유 사용자의 비밀번호 변경으로 자격증명 경계(계약 1, 토큰 `iat` < `users.password_changed_at`)를 넘긴 경우 중 하나라도 성립하면 서버가 해당 연결을 능동 종료한다. → 폐기된 뷰어의 WS가 토큰 만료(최대 24h)까지 `crisis_alert`/`incident_resolved`를 계속 수신하는 일이 발생하지 않는다. (권장 구현 노트: 별도 재검증 타이머를 두기보다 서버의 30초 ping 사이클(아래)에 편승하면 충분하다 — 계약 상한 `N ≤ 60`은 유지하되 별도 타이머는 과설계다.)
- **admin 접속 시 unhealthy 스냅샷 재전달(⚠ 권장, 계약 12)**: admin role WS가 새로 접속하면 `connected` 직후, 현재 `unhealthy`인 모든 감시 대상에 대해 `system_alarm`(`details.status=="unhealthy"`)을 그 admin에게만 재전달한다 — 전이 순간 미접속이던 admin의 통지 유실을 보정한다.
- 서버가 30초마다 ping — 클라이언트는 40초 내 pong 없으면 연결 종료
- role은 JWT의 `role` 클레임에서 추출 (`admin`/`user`). temp link 토큰은 role 클레임이 없으나 WS 접면이 temp 종류로 식별해 role `"temp"`(read-only)를 부여한다 — `system_alarm`은 admin에게만 전달되고 temp/user에는 전달되지 않는다
- `crisis_alert`는 `POST /api/incidents`(계약 13) 성공 시, `incident_resolved`는 웹 resolve(계약 2) 또는 센서 resolve(계약 13) 성공 시 발생. `system_alarm`은 (i) `POST /internal/alarms`(계약 13, notifier가 모든 외부 채널 실패 시 호출) 수신 시, 또는 (ii) 감시 대상(서비스/센서)의 healthy↔unhealthy 상태 전이 시(계약 12) admin에게만 발생
- **`device_reappeared` 발행·backfill(계약 6·13, admin 전용)**: soft-deleted 장치가 `seen`/`incidents`로 재신호한 삭제→재출현 사이클 최초 시점에 1회 브로드캐스트된다(재출현 dedup 가드). 또한 admin WS가 새로 접속하면 `connected` 직후 현재 삭제 상태이면서 이미 경보된(`deletedAt IS NOT NULL AND reappear_alerted_at IS NOT NULL`) 모든 장치에 대해 `device_reappeared`를 backfill 재전달한다 — 전이 순간 미접속이던 admin의 유실 보정(unhealthy 스냅샷 재전달과 동형)

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

## 계약 16: Notifications — 채널별 테스트 발송 (`/api/notifications/*`)

관리자가 저장해 둔 알림 채널(이메일·SMS)이 실제로 동작하는지 **채널마다 한 번씩** 확인하는 접면. 단위 계약의 정본은 `docs/spec/notification-test-send.md`이며, 본 계약은 그 web-api 접면을 고정한다. 채널 사용가능 판정과 실제 발송은 **동일 소스(notifier 실행 config)** 를 참조한다 — web-backend는 notifier 조회 결과를 **그대로 투사**하고 자체 판정하지 않는다. notifier internal 접면(`/internal/channel-status`·`/internal/test-send`)은 `docs/spec/notifier.md`가 소유한다.

### 입력

| Method | Path | Auth | 입력 |
|--------|------|------|------|
| GET | `/api/notifications/channels` | admin | — (in-scope 채널별 사용가능 상태 조회) |
| POST | `/api/notifications/test` | admin | `{channel, target}` — `channel ∈ {email, sms}`, `target`은 관리자가 입력한 명시 단일 값(이메일 주소/전화번호; contactId 아님) |

- **지원 채널 집합은 정확히 `{email, sms}`**. `channel ∉ {email, sms}`(예: `kakaotalk`)은 `400`(발송 0건).
- `target` 형식: email은 `local@domain.tld`, sms는 전화번호 `01[016789]-\d{3,4}-\d{4}`. 형식 위반·부재는 `400`(발송 0건).

### 출력 (계약)

`GET /api/notifications/channels` → `200`:

```json
{ "email": { "usable": true, "reason": "" }, "sms": { "usable": false, "reason": "not_configured" } }
```

- 채널 집합은 **정확히 `email`·`sms` 두 키뿐**이다 — `kakaotalk` 키는 존재하지 않는다.
- `usable`은 web-backend의 자체 추측이 아니라 **notifier의 현재 실행 config 조회 결과를 그대로 투사**한 것이다(status 소스 = 발송 소스 = notifier 동일).
- notifier 무응답/비-200(중단·재시작 창) → `502` `{"error": "...", "reason": "upstream_unavailable"}`. **거짓 `not_configured`(또는 이유 없는 `usable=false`)로 강등하지 않는다** — 미설정과 도달불가는 구분되는 사유다.

`POST /api/notifications/test` → `200`:

```json
{ "outcome": "sent" }          // 또는 { "outcome": "failed", "reason": "..." } · { "outcome": "not_configured" }
```

- `outcome ∈ {sent, failed, not_configured}`(폐집합). `failed`는 `reason`을 동반한다.
  - `sent` — 실제 발송 경로로 시도했고 채널/전송이 수락.
  - `failed`(+`reason`) — 실제 발송 경로로 시도했으나 전송/공급자 오류.
  - `not_configured` — 요청 시점 판정상 채널 미설정(필수 자격 값 부재) — **발송을 시도하지 않는다**. (이메일은 notifier `POST /api/send-email`의 `503`(SMTP 미설정)이 `not_configured`로 매핑된다.)
- **처리 순서**: admin 관문 → 입력/채널 지원 검증(`400`, **레이트리밋보다 먼저**·`400`은 토큰 미소모) → `(channel, target)` 레이트리밋(`429`, 발송 0건) → notifier `/internal/test-send` 프록시.
- `channel ∉ {email, sms}`(예: `kakaotalk`) → `400`(발송 0건). 입력 형식 위반 → `400`(발송 0건). 이 `400`들은 레이트리밋 토큰을 소모하지 않으므로 직후의 유효 요청은 `429`가 되지 않는다.
- 같은 `(channel, target)`로 분당 1건 초과 → `429`(발송 0건). 스코프가 `(channel, target)`이므로 다른 `target` 요청은 이 리밋에 걸리지 않는다.
- notifier 자체 미도달/5xx → `502` `{"reason": "upstream_unavailable"}`. **폐집합 `{sent, failed, not_configured}` 어디에도 맞지 않으므로 outcome을 만들지 않는다** — `not_configured`/`sent`로 강등하지 않는다.

### 핵심 로직 (불변식)

- **투사(projection)**: web-backend는 채널 usability를 자체 판정하지 않고 notifier의 `/internal/channel-status` 결과를 그대로 투사한다. 응답 구조체가 `email`·`sms`만 담아 notifier가 다른 채널 키를 실어도 표면화되지 않는다(KakaoTalk 부재).
- **status 소스 = 발송 소스**: 사용가능 상태 조회와 실제 발송 분기(미설정/설정)는 notifier의 동일 config를 참조하므로 어긋날 수 없다. notifier config는 배포 시점 env이나, notifier 재시작 후에는 **web-backend 재시작 없이** 다음 요청부터 새 판정이 반영된다.
- **발송 대상 격리(스팸 방지)**: 테스트는 관리자가 지정한 단일 대상 1건에게만 도달한다 — 등록 비상연락처(contactId)로의 팬아웃 0건. 위기 경로의 fallback 체인(KakaoTalk→SMS→시스템알람)과 연락처 팬아웃은 우회하되, 운영 발송기의 자격증명·전송 구현은 공유한다.
- **채널 독립**: 이메일 테스트는 이메일만, SMS 테스트는 SMS만 시도한다(교차 발동 없음).
- **프록시 홉 타임아웃 규율**: web-backend→notifier 프록시 홉(channels·test 모두)의 클라이언트 타임아웃은 채널 예산(≤12s, `notifier.md` §출력 7)보다 크게(≥15s) 둔다 — notifier의 정상 지연 응답을 조기 `502`로 오종결하지 않기 위함. 진짜 도달불가만 `502`로 종결.
- **권한**: 두 표면 모두 **연락처 CUD(POST/PUT/DELETE `/api/contacts`)와 동일한 admin 권한**을 요구한다(`GET /api/contacts`의 user 권한과 구분). 비-admin `403`, 무인증 `401` — 어느 경우에도 발송 0건.

### 검증 단언 (TDD)

- [ ] `GET /api/notifications/channels`(admin) → `200`, 키 집합이 정확히 `{email, sms}`(kakaotalk 없음), 각 채널에 `usable`(boolean)·`reason` 존재
  ```bash
  curl -s -H "Authorization: Bearer $ADMIN" http://localhost:8080/api/notifications/channels \
    | jq -e '(keys|sort)==["email","sms"] and (.email|has("usable")) and (.sms|has("usable"))'
  ```
- [ ] `POST /api/notifications/test` `{"channel":"kakaotalk","target":"..."}` → `400`(발송 0건)
- [ ] 미설정 이메일 채널로 `POST /api/notifications/test` `{"channel":"email","target":"a@b.c"}` → `200` `{"outcome":"not_configured"}`(발송 0건)
- [ ] user 토큰으로 `GET /api/notifications/channels`·`POST /api/notifications/test` → `403`; 무인증 → `401`(발송 0건)
- [ ] 같은 `(channel, target)`로 분당 2번째 유효 요청 → `429`(발송 0건); 다른 `target`은 비-`429`
- [ ] 입력 형식 위반(`400`) 직후 같은 `(channel, target)`의 유효 요청 → `429`가 아님(400은 토큰 미소모)
- [ ] notifier 중단 상태에서 `GET /api/notifications/channels`·`POST /api/notifications/test` → `502` + `reason == "upstream_unavailable"`(거짓 `not_configured`/`sent` 강등 없음)

---

## ⚠️ 리뷰 필요 (문서-코드 불일치)

실제 라우트/핸들러 대조에서 발견된 어긋남. 본 스펙 본문에는 반영하지 않고 여기에만 기록한다.

1. **~~notifier의 시스템 알람 최후 보루 수신측 부재~~ (해소됨)** — 무인증 internal 라우트 `POST /internal/alarms`가 신설되어 notifier가 이 경로로 호출하고, web-backend가 수신해 WS `system_alarm`(계약 14)을 admin에게 브로드캐스트한다. 계약 13(입력/출력)과 계약 14(WS 발생 경로)에 반영됨. DB 영속은 없으며 보장 동작은 "수신 + admin 브로드캐스트"까지. (번호는 항목 2~4의 교차참조 안정성을 위해 유지)
2. **~~temp link JWT가 WS에서 temp role 식별·회수 차단·만료 재검증을 우회~~ (WS 접면 해소됨 — 계약 14, 이슈 #82)** — WS 접면은 이제 접속 시점에 temp 토큰을 temp 종류로 식별해 role `temp`를 부여하고 blacklist를 확인하며(회수 토큰 업그레이드 `401`), 수립된 연결은 주기적(≤60초) 재검증(회수·`exp` 만료·비밀번호 변경 경계, 계약 1)으로 무효 토큰 연결을 능동 종료한다. 관련 단언("WS `payload.role == "temp"`"·"회수 토큰 WS 접속 `401`"·"회수/비번변경 후 능동 종료")을 계약 14에 추가했다. **남은 사안(미해소)**: `/api/*` 인증 미들웨어 접면에서 temp 토큰이 일반 JWT 파서를 먼저 통과해 blacklist 확인 없이 `role=""`로 통과하는 문제는 web-backend 스펙 ⚠️1(authz 범위)로 별도 추적한다 — WS 재검증 계약과는 접면이 다르다.
3. **무인증 internal 엔드포인트가 리버스 프록시를 그대로 통과해 외부 노출됨 (알려진 시스템 갭 — 부분 완화)** — 현 스택의 유일한 리버스 프록시인 web-frontend nginx는 `/api/` 전체를 web-backend로 무차별 프록시하고, docker-compose가 web-frontend:80을 호스트 `3080`으로 노출한다. **`POST /api/devices/seen`·`POST /api/incidents` 두 경로는 이제 앱레벨 `X-Internal-Token` fail-closed 게이트로 방어**되어(계약 13), 유효 토큰 없는 외부 호출은 `401`이다(heartbeat 유입·위기 자동등록의 무인증 오남용 경계 차단). 그러나 나머지 internal 경로(계약 13의 `POST /api/incidents/{id}/resolve-from-sensor`, `/internal/*` 카메라 reload/list·contacts·settings, 계약 9의 internal 폴백 `POST /api/links/temp`)는 여전히 무인증으로 그대로 호출된다(200/201). 이들로의 토큰 게이트 확장은 시스템 후속 과제로 남는다(현재 방어는 `seen`·`incidents` 두 경로 한정 — 과대주장 금지). → 의도 결정 필요: (a) 리버스 프록시에서 해당 경로 차단(location 규칙), (b) internal 엔드포인트를 `/internal/*` 프리픽스로 이동 + 프록시 미노출, 또는 (c) 내부 호출 인증(토큰 게이트) 나머지 경로로 확장. 결정 전까지 "외부에서 (미방어) internal 엔드포인트 `4xx`" 배포 게이트 단언은 계약에 두지 않는다.
4. **`PUT /api/contacts/{id}`의 email만 partial 시맨틱에서 벗어남 (실측 — web-backend 스펙 ⚠️5와 동일 사안)** — name/phone은 빈 값 무시, notifyEmail은 생략 시 유지인데 email은 요청값으로 무조건 덮어써서, email을 생략한 partial PUT이 기존 email을 NULL로 삭제한다. 본문(계약 5)은 이 실측 동작을 계약으로 기술했다. → 의도 결정 필요: (a) email도 생략 시 유지(partial 통일 — email 삭제는 별도 신호 필요), 또는 (b) 현 덮어쓰기 동작 유지. (a)로 결정되면 계약 5 입력 표·불변식·단언을 함께 갱신해야 한다.
5. **[해소 — 설계자 결정] unhealthy 자동 복구 범위.** unhealthy 상태 전이의 admin 통지(계약 12 → 계약 14 `system_alarm`)까지만 앱이 계약한다. hang 상태 서비스의 자동 컨테이너 재시작은 인프라 계층(docker compose restart policy / healthcheck 연동) 책임으로 두어 앱 계약에서 제외하며, unhealthy 전이의 notifier 외부 채널 팬아웃도 채택하지 않는다.
