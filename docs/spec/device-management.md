# 장치(카메라·센서) 등록·삭제·교체 스펙

> 상태: 살아있는 계약 (living spec) · 독자: 스펙 작성자 / 오케스트레이터
> 접합부: **web-frontend ↔ web-backend**, **web-backend ↔ (cctv-adapter · youtube-adapter · recording)**, **hw-gateway ↔ web-backend**
> 본 문서는 CCTV 카메라와 음성센서(장비)의 **등록·삭제·교체 수명주기**를 계약으로 고정한다.
> Web API 접면의 전면 SSOT는 `docs/spec/interface-web-api.md`이며, 본 문서는 그 계약 중
> "장치 관리" 단위의 **의도 변경분(센서 명시등록·삭제 영구화·카메라 reload 팬아웃 확장·삭제 증거보존)**을 규정한다.
> 두 문서가 충돌하면 본 단위 범위(장치 등록/삭제/교체) 안에서는 본 문서가 최신 의도를 갖는다 —
> 구현 중 `interface-web-api.md`·`web-backend.md`·`hw-gateway.md`·`recording.md`를 본 계약에 맞춰 정합화한다(아래 "델타" 참조).

## 목적 / 의도

현장에는 CCTV 카메라와 음성센서가 **각각 N개 존재하며 교체가 일어난다**. 이 단위는 두 장치 클래스의 추가·삭제·교체를 판정 가능한 계약으로 고정하여 네 가지를 보장한다.

1. **교체는 별도 기능이 아니다** — 물리 장치를 갈아끼우는 "교체(replace)"는 **운영자의 의도적 삭제 + 추가**로 수행된다. 시스템에 "replace" 전용 액션·엔드포인트·마법사는 존재하지 않는다. 카메라·센서 공통.
2. **센서도 명시적으로 관리된다** — 센서는 heartbeat 자동발견(auto-discovery)으로 등장하되, 자동발견이 놓치는 경우를 위해 **운영자가 명시적으로 등록**할 수 있다. 삭제는 기본적으로 **관대(forgiving)** 하여 재신호 시 자동 복원되고(일시 에러·오삭제 대비), 진짜 제거가 필요할 때만 **차단(자동 재등록 제외)** 을 옵트인한다 — 운영자는 이 **차단 목록만 관리**한다.
3. **카메라 변경은 녹화까지 즉시 전파된다** — 카메라 추가·수정·삭제는 스트리밍뿐 아니라 **녹화 서비스에도 즉시 반영**된다(재시작 없이).
4. **삭제는 증거를 지우지 않는다** — 카메라 삭제는 그 카메라와 연관된 **보호 아카이브·사고(incident) 증거를 삭제하지 않는다**. 교체로 인한 라이브 이력 표시 단절은 수용하되, 증거 자체는 보존되어 사고번호·발생일시·streamKey로 조회 가능하다.

배경 의도: 카메라는 이미 명시적 CRUD가 있으나 센서는 자동발견만 가능해 "교체=삭제+추가" 패턴이 반쪽이었다. 센서에 명시등록과 차단 목록(옵트인 제거)을 부여해 두 클래스의 관리 모델을 대칭화한다. 삭제를 기본 관대·차단만 영구로 두어, 일시 에러로 지운 장치를 다시 살리는 마찰을 없애면서 진짜 제거도 가능하게 한다. 아울러 카메라 변경의 녹화 미반영 갭을 봉합하고, 산업안전 시스템으로서 장치 삭제가 과거 증거를 소실시키지 않음을 계약으로 못박는다.

## 언어 · 런타임

- **web-backend**: Go (표준 `net/http`, Go 1.22+ 메서드 라우팅), 포트 `:8080`
- **web-frontend**: TypeScript + React (Vite), 브라우저 `fetch`
- **hw-gateway**: Go, MQTT 구독자 (heartbeat 자동발견 통지)
- **recording**: Go, 카메라 목록 소비자(reload 수신)
- **직렬화**: JSON (`Content-Type: application/json`)

## 의존 도구 · 시스템

- **SQLite** — `devices` 테이블(센서 영속 등록: `site_id`·`device_id`·`alias`·`first_seen`·`last_seen`·`deleted_at`, `UNIQUE(site_id, device_id)`), `cameras` 테이블(카메라 등록, 서버 발급 `stream_key` 불변), `incidents`·아카이브 메타(증거).
- **JWT (HS256)** — 관리 엔드포인트 인증. role: `admin`(추가/삭제) · `user`(조회) · `temp`(read-only).
- **MQTT** — `safety/+/heartbeat` 수신 시 hw-gateway가 web-backend로 자동발견 통지(단방향·최선노력).
- **내부 HTTP reload** — web-backend → `POST {cctv-adapter}/api/cameras/reload` · `POST {youtube-adapter}/api/cameras/reload` · `POST {recording}/api/cameras/reload`. 소비자는 수신 시 `GET /internal/cameras`를 재조회해 실행 상태를 재조정(reconcile)한다.

---

## 계약 1 — 센서 명시 등록 (web-backend)

### 입력

| Method | Path | Auth | 입력 |
|--------|------|------|------|
| POST | `/api/devices` | admin | `{siteId: string, deviceId: string, alias?: string}` — `siteId`·`deviceId` 필수(비어있지 않음) |

### 출력 (계약)

- 신규 `(siteId, deviceId)`: `201`(또는 `200`), 등록된 장치 표현 반환. 등록 직후 상태는 **오프라인 대기** — `last_seen`은 미신호 상태(예: `null`)이고 `deleted_at`은 `null`이다. heartbeat가 도착하면 온라인으로 전이한다.
- 이미 존재하고 **미삭제**인 `(siteId, deviceId)`: `409`(중복 등록 방어).
- 이미 존재하나 **tombstone(삭제)** 상태인 `(siteId, deviceId)`: 그 행을 **되살려**(deleted_at 해제) 등록으로 확정한다 — 명시적 재등록은 자동발견과 달리 의도된 복원이다(`200`/`201`).

### 핵심 로직 (동작)

- 명시 등록은 heartbeat 없이도 장치를 레지스트리에 넣는다(프로비저닝·자동발견 누락 대비).
- 명시 등록은 관리 액션이므로 admin 전용이다.
- 명시 재등록(tombstone 키에 대한 POST)은 **의도된 복원 경로**로 취급한다 — 자동발견의 "부활 안 함"(계약 2) 규칙과 구별된다.

---

## 계약 2 — 자동발견 · 관대한 삭제 · 차단 목록 (web-backend ↔ hw-gateway)

### 입력

| Method | Path | Auth | 입력 |
|--------|------|------|------|
| POST | `/api/devices/seen` | none (internal) | hw-gateway 자동발견 통지 — `{siteId, deviceId, ...}` (heartbeat 파생) |
| DELETE | `/api/devices/{id}` | admin | 선택 플래그 `block`(예: 바디 `{block?: boolean}` 또는 `?block=true`, 기본 `false`) |
| POST | `/api/devices/{id}/restore` | admin | — (복원) |
| POST | `/api/devices/{id}/unblock` | admin | — (차단 해제) |
| GET | `/api/devices/blocked` | admin | — (차단 목록 조회) |

### 출력 (계약)

- `POST /api/devices/seen`:
  - 미지 `(siteId, deviceId)` → **자동 등록**(신규 행 생성, 온라인).
  - 기지 & 미삭제 → `last_seen` 갱신(온라인 유지).
  - 기지 & soft-delete & **미차단** → **자동 복원**(재신호로 목록에 되돌아옴 — 일시 에러·오삭제에 관대).
  - 기지 & **차단(blocked)** → **복원하지 않는다.** 차단 상태·목록 미노출이 유지되고, 이 통지로 부활하지 않는다.
- `DELETE /api/devices/{id}`:
  - 기본(`block` 미지정/`false`) → **관대한 soft-delete**: 목록에서 제거하되 재신호 시 자동 복원 가능.
  - `block=true` → soft-delete + **차단 목록 등록**: 이후 자동발견 통지로 부활하지 않는다.
- `POST /api/devices/{id}/unblock` → 차단 해제. 이후 자동발견 복원이 다시 허용된다.
- `GET /api/devices/blocked` → 현재 차단(자동 재등록 제외) 대상 목록. 운영자는 이 목록만 관리한다.

### 핵심 로직 (동작)

- **삭제는 기본 관대(forgiving)** — 일반 삭제는 목록에서 빼되, 같은 장치가 재신호하면 자동 복원된다. 일시적 에러나 오삭제 후 재등록의 마찰을 없앤다.
- **차단은 옵트인 제거** — 진짜 제거(아직 살아 신호를 보내는 장치 포함)를 원할 때만 차단한다. 차단된 `(siteId, deviceId)`는 자동발견이 등록·복원하지 않는다. "교체=삭제+추가"의 영구 제거 경로.
- **자동발견 규칙** — 미지 장치는 새로 등록하고, **미차단** soft-deleted 장치는 재신호로 복원하며, **차단**된 것만 예외로 부활시키지 않는다.
- **삭제/차단 ≠ 안전기능 정지** — 장치 삭제·차단은 **감시 목록(등록 레지스트리)에서 제외**하는 것이지, 그 장치가 발행하는 위기·후보 경보의 처리를 끄는 것이 아니다. 안전경보 파이프라인(경보 생성·전달)은 등록/차단 상태와 독립적으로 계속 동작한다.
- **살아있는 차단 장치의 가시성(수용된 트레이드오프)** — 차단된 장치가 아직 신호를 보내더라도 기본 목록에는 나타나지 않고 차단 목록으로만 관리된다. 다시 보이게 하려면 운영자가 차단 해제(그리고 재신호/명시 등록으로 복원)한다.

---

## 계약 3 — 카메라 변경의 녹화 전파 (web-backend → recording)

### 입력

| Method | Path | Auth | 입력 |
|--------|------|------|------|
| POST/PUT/DELETE | `/api/cameras` · `/api/cameras/{id}` | admin | 기존 카메라 CRUD (본 단위는 스키마 불변) |

### 출력 (계약)

- 카메라 생성·수정·삭제가 성공하면, web-backend는 스트리밍 소비자(cctv-adapter·youtube-adapter)뿐 아니라 **recording에도 `POST /api/cameras/reload`를 팬아웃**한다. recording은 이를 수신해 `GET /internal/cameras`를 재조회하고 녹화 프로세스를 재조정한다(추가된 카메라 녹화 시작, 삭제된 카메라 녹화 중단).

### 핵심 로직 (동작)

- 카메라 CRUD의 reload 팬아웃 대상은 **cctv-adapter · youtube-adapter · recording** 세 소비자다. 어느 하나라도 누락되면 그 소비자는 재시작 전까지 변경을 반영하지 못한다.
- reload 팬아웃은 최선노력·비동기다 — CRUD 응답은 팬아웃 완료를 기다리지 않는다(개별 소비자 실패가 CRUD를 실패시키지 않는다).

---

## 계약 4 — 카메라 삭제의 증거 보존 (web-backend · recording)

### 입력

| Method | Path | Auth | 입력 |
|--------|------|------|------|
| DELETE | `/api/cameras/{id}` | admin | — |

### 출력 (계약)

- 카메라 삭제는 `cameras` 레지스트리에서 그 카메라만 제거한다. 그 카메라의 `stream_key`에 연관된 **보호(protected)·finalize된 아카이브**와 **사고(incident) 레코드**는 **삭제되지 않는다.** 삭제 후에도 사고 레코드는 조회 가능하고, 보호 아카이브 증거는 보존된다.

### 핵심 로직 (동작)

- 카메라 삭제는 증거 저장소(사고 테이블·보호 아카이브)로 **연쇄(cascade)하지 않는다.**
- 교체로 인한 라이브 이력 표시 단절(새 카메라는 새 `stream_key`를 받으므로 과거 이력이 새 카메라 행에 자동 연결되지 않음)은 **수용**한다. 그러나 증거 자체는 사고번호·발생일시·`stream_key` 문자열로 여전히 조회 가능하다.
- (경계) 보호되지 않은 롤링 세그먼트는 기존 롤링 윈도우 정책대로 자연 만료될 수 있다 — 이는 삭제와 무관한 정상 동작이며 본 계약은 **보호·finalize된 증거**의 보존만 규정한다.

---

## 계약 5 — 장치 추가 UI (web-frontend)

### 출력 (계약)

- 센서/장비 관리 화면(장비 목록 영역)에 **"장치 추가" 액션**이 존재하여 `siteId`·`deviceId`(및 선택 별칭)를 입력해 계약 1의 `POST /api/devices`를 호출한다. 카메라 관리의 "추가"와 대칭이다.
- 장치 삭제 시 **"자동 재등록에서 제외(차단)" 옵션**이 제공되어, 켜면 `DELETE`가 `block=true`로 호출된다(끄면 관대한 삭제). 삭제 안내 문구는 두 경로를 정확히 반영한다 — 기본 삭제는 재신호 시 복원될 수 있고, 차단해야 영구 제외됨을 알린다.
- **차단 목록** 관리 화면이 존재하여 현재 차단된 장치를 보고 **차단 해제**(`POST /api/devices/{id}/unblock`)할 수 있다.

### 핵심 로직 (동작)

- 장치 추가·삭제·차단·차단해제는 관리자 대상 액션이다(비-admin에겐 미노출 또는 비활성).

## 검증 단언 (TDD)

- **A (핵심)** — 명시 등록: 신규 `(siteId, deviceId)`에 대한 `POST /api/devices`(admin) → `2xx`. 이후 `GET /api/devices`에 그 장치가 나타나고 **오프라인 대기**(미신호: `last_seen`이 온라인 임계 밖 또는 미설정)이며 `deleted_at`이 없다.
  ```bash
  curl -s -X POST -H "Authorization: Bearer $ADMIN" -H 'Content-Type: application/json' \
    http://localhost:8080/api/devices -d '{"siteId":"site-001","deviceId":"vs-new-01","alias":"북문 음성센서"}' \
    -o /dev/null -w '%{http_code}\n'   # → 201 또는 200
  curl -s -H "Authorization: Bearer $ADMIN" http://localhost:8080/api/devices \
    | jq -e 'any(.[]; .siteId=="site-001" and .deviceId=="vs-new-01")'
  ```
- **B** — 중복 방어: 이미 미삭제로 존재하는 `(siteId, deviceId)`에 `POST /api/devices` → `409`.
- **C** — 명시 재등록(의도된 복원): tombstone된 `(siteId, deviceId)`에 `POST /api/devices` → `2xx`이고 이후 그 장치가 미삭제(`deleted_at` 없음)로 목록에 나타난다.
- **D (핵심, mutating)** — 자동발견: 미지 `(siteId, deviceId)`로 `POST /api/devices/seen` → 그 장치가 `GET /api/devices`에 온라인으로 자동 등록된다.
- **E1 (핵심, mutating)** — 관대한 삭제 후 자동 복원: (1) `DELETE /api/devices/{id}`(block 미지정) → (2) 같은 `(siteId, deviceId)`로 `POST /api/devices/seen`(재신호) → (3) 그 장치가 기본 `GET /api/devices`(미삭제 목록)에 **다시 나타난다**(자동 복원).
  ```bash
  curl -s -X DELETE -H "Authorization: Bearer $ADMIN" http://localhost:8080/api/devices/$ID
  curl -s -X POST http://localhost:8080/api/devices/seen -H 'Content-Type: application/json' \
    -d '{"siteId":"'"$SITE"'","deviceId":"'"$DEV"'"}'   # 재신호
  curl -s -H "Authorization: Bearer $ADMIN" http://localhost:8080/api/devices \
    | jq -e 'any(.[]; .deviceId == "'"$DEV"'")'   # 복원됨
  ```
- **E2 (핵심, mutating)** — 차단 삭제 후 부활 금지: (1) `DELETE /api/devices/{id}` **`block=true`** → (2) 같은 `(siteId, deviceId)`로 `POST /api/devices/seen`(재신호) 1회 이상 → (3) 기본 `GET /api/devices`에 **나타나지 않고**, `GET /api/devices/blocked`에는 그 장치가 존재한다(자동 부활 없음).
  ```bash
  curl -s -X DELETE -H "Authorization: Bearer $ADMIN" 'http://localhost:8080/api/devices/'"$ID"'?block=true'
  curl -s -X POST http://localhost:8080/api/devices/seen -H 'Content-Type: application/json' \
    -d '{"siteId":"'"$SITE"'","deviceId":"'"$DEV"'"}'   # 재신호
  curl -s -H "Authorization: Bearer $ADMIN" http://localhost:8080/api/devices \
    | jq -e 'all(.[]; .deviceId != "'"$DEV"'")'   # 기본 목록엔 부활 안 함
  curl -s -H "Authorization: Bearer $ADMIN" http://localhost:8080/api/devices/blocked \
    | jq -e 'any(.[]; .deviceId == "'"$DEV"'")'   # 차단 목록엔 존재
  ```
- **E3 (mutating)** — 차단 해제 후 복원 허용: E2 상태(차단됨)에서 `POST /api/devices/{id}/unblock` → 재신호 `POST /api/devices/seen` → 그 장치가 기본 `GET /api/devices`에 복원된다.
- **F** — 자동발견 갱신: 이미 미삭제로 존재하는 장치에 `POST /api/devices/seen` → `last_seen`이 갱신되어 온라인 판정된다(새 행 생성 없이 기존 행 갱신).
- **G (핵심, mutating)** — 삭제≠안전정지: tombstone된 장치의 `siteId`로 위기 경보 이벤트가 유입되면 사고(incident)는 정상 생성/전달된다 — 장치 삭제가 그 장치발 안전경보 처리를 막지 않는다(등록 상태와 독립).
- **H (핵심, mutating)** — 카메라 reload 팬아웃에 recording 포함: 카메라 생성·수정·삭제 각각에 대해 recording의 `POST /api/cameras/reload`가 호출된다(수신 관측 또는 recording의 카메라 목록 재조정으로 판정). cctv-adapter·youtube-adapter 팬아웃과 병렬로 recording도 트리거된다.
  ```bash
  # 격리 스택에서 recording reload 수신을 계측(mock 수신기 또는 recording 로그/상태 관측):
  # 카메라 1건 생성 → recording이 GET /internal/cameras 재조회 후 새 streamKey 녹화 시작을 관측
  ```
- **I (핵심, mutating)** — 삭제 증거 보존: 어떤 카메라의 `stream_key`에 연관된 사고 레코드와 **보호(protected) 아카이브**가 존재하는 상태에서 그 카메라를 `DELETE /api/cameras/{id}` → 삭제 후에도 (a) 그 사고 레코드가 `GET /api/incidents`로 조회되고 (b) 보호 아카이브가 여전히 존재한다(삭제로 연쇄 제거되지 않음).
- **J (핵심, needs-browser SKIP — 의도적)** — 장치 관리 UI: web-frontend 장비 관리 화면에 "장치 추가" 액션이 렌더되어 `POST /api/devices`를 호출하고, 삭제 시 "자동 재등록 제외(차단)" 옵션이 제공되며(켜면 `DELETE`가 `block=true`), 차단 목록 화면에서 차단 해제가 가능하다. 삭제 안내 문구가 두 경로(기본=재신호 시 복원 가능 / 차단=영구 제외)를 정확히 반영한다. 브라우저 관측이 필요하므로 needs-browser SKIP으로 선언한다.
- **K** — 권한: user 토큰으로 `POST /api/devices` → `403`(명시 등록은 admin 전용). 조회 `GET /api/devices`는 user `200`.

## 검증 스킵 선언 (선택)

- **D · E1 · E2 · E3 · F · G** — 사유: mutating 단언 — `/api/devices/seen` 자동발견 통지와 삭제·차단·차단해제 전이는 `devices` 테이블/차단 목록에 부작용을 남기고, G는 실 위기 경보 유입이 필요하다. **격리 스택 + `ADMIN_TOKEN`** 에서 판정한다(공유 운영 DB 오염 금지). · 중요도: **핵심(load-bearing)** (관대한 삭제·차단·자동발견 대칭이 이 단위의 핵심 보장) · 기본 **SKIP**, 격리 스택 준비 시 판정.
- **H** — 사유: mutating·다중서비스 — 카메라 CRUD가 recording까지 팬아웃되는지는 recording 인스턴스(+수신 계측)가 있는 격리 스택이 필요하다. · 중요도: **핵심(load-bearing)** · 해소 조건: recording 포함 격리 스택 + reload 수신 계측.
- **I** — 사유: mutating·다중서비스 — 사고+보호 아카이브 시딩 후 카메라 삭제의 증거 보존을 관측하려면 recording 볼륨/아카이브가 있는 격리 스택이 필요하다. · 중요도: **핵심(load-bearing)** (산업안전 증거 보존이 이 단위의 4대 보장 중 하나) · 해소 조건: recording 포함 격리 스택 + 사고/아카이브 픽스처.
- **J** — 사유: 브라우저 렌더 관측 필요(web-frontend needs-browser). 장치추가 UI·삭제 문구 정합은 API로 판정 불가. · 중요도: **핵심(load-bearing)** · 해소 조건: Playwright 세션 실행 시 판정(INDEX.md SKIP 해제 조건 4와 동류).

## 델타 (SSOT 정합 — 오케스트레이터 머지 반영분)

> **편집 경계 (SSOT 위임)**: 본 잎은 **코드 + 자기 `tests/` + 본 스펙 문서**를 편집한다. 아래 인터페이스/서비스 SSOT 정합은 오케스트레이터가 머지 시점에 반영한다(잎 워크트리 간 SSOT 충돌 방지).

- **`interface-web-api.md`**: `POST /api/devices`(admin, 센서 명시 등록), `DELETE /api/devices/{id}`의 선택 `block` 플래그, `POST /api/devices/{id}/unblock`, `GET /api/devices/blocked` 계약 추가. 카메라 CRUD의 reload 팬아웃 소비자 목록에 **recording** 추가. `POST /api/devices/seen` 자동발견의 규칙(미차단 soft-deleted는 복원, 차단은 부활 금지) 명시.
- **`web-backend.md`**: devices 절에 명시 등록(`POST /api/devices`)·관대한 삭제(기본 재신호 복원)·차단 목록(옵트인 영구 제외, `block`/`unblock`/`/blocked`) 계약 반영. 카메라 삭제의 증거 비연쇄(사고·보호 아카이브 미삭제) 가드 명시.
- **`hw-gateway.md`**: 자동발견 통지가 web-backend에서 **차단된** 장치를 부활시키지 않음(미차단 soft-deleted는 복원됨)을 상호작용 계약으로 반영(hw-gateway는 heartbeat 통지자, 복원/차단 판정 소유는 web-backend).
- **`recording.md`**: `POST /api/cameras/reload` 수신 시 카메라 목록 재조정을 카메라 CRUD 전파의 정식 소비자로 반영(기존 시작-시 1회 조회에서, web-backend 팬아웃 수신 소비자로 승격).
