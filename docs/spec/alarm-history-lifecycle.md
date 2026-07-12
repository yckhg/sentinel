# 경보 이력 + 처리 상태머신 스펙

> 상태: 살아있는 계약 (living spec) · 독자: 스펙 작성자 / 오케스트레이터
> 접합부: **web-backend ↔ web-frontend** (+ web-backend ↔ hw-gateway 센서 해소 경로)
> 본 문서는 위급 이벤트(내부 엔티티명 `incident`)의 **처리 수명주기**와 **사용자 대면 표기**를 계약으로 고정한다.
> Web API 접면의 전면 SSOT는 `docs/spec/interface-web-api.md`(계약 2·13·14)이며, 본 문서는 그 계약 중
> "경보 이력 + 처리 상태머신" 단위의 **의도 변경분(상태집합 축소·해제노트 선택화·표기 리네임)**을 규정한다.
> 두 문서가 충돌하면, 본 단위 범위(상태집합/해제/표기) 안에서는 본 문서가 최신 의도를 갖는다 —
> 구현 중 `interface-web-api.md`를 본 계약에 맞춰 정합화한다(아래 "API 계약 델타" 참조).

## 목적 / 의도

위급 경보(hazardous crisis)가 발생한 뒤 **운영자가 이를 처리(해소)하기까지의 수명주기**를 판정 가능한 계약으로 고정한다. 이 단위는 세 가지를 보장한다.

1. **표기 계약**: 사용자 대면 표면(이력 탭/네비게이션/페이지 제목)의 표기는 **"경보"**(알림/경보)다. "사고"가 아니다. 이는 **UI 카피 계약**일 뿐이며 내부 엔티티명·API 경로·DB 테이블·WS 타입은 모두 `incident` 계열을 유지한다.
2. **상태머신 계약**: 처리 상태 집합은 **`{open, resolved}`** 두 값뿐이다. 중간 확인 상태(`acknowledged`)와 확인(acknowledge) 액션/엔드포인트는 계약에서 제거된다. `open` 경보는 확인 단계 없이 **직접 해소**된다.
3. **해제 부작용 보존**: 경보 해소는 여전히 (a) 아카이브 finalize, (b) hw-gateway/센서 동기화, (c) WS `incident_resolved` 브로드캐스트를 수반하며, 센서 물리 버튼 해소 경로도 그대로 `open → resolved`로 동작한다.

배경 의도: 현장 운영에서 "확인 후 해소" 2단계는 실사용되지 않아 제거하고, 해소 시 사유(note) 입력을 **선택**으로 두어 즉시 해소를 마찰 없이 만든다. 동시에 사용자가 마주하는 어휘를 "사고(incident)"에서 "경보(alarm/alert)"로 통일해, 실제로 사고가 아닌 조기 경보 상황에서의 표현 오해를 없앤다.

## 언어 · 런타임

- **서버**: Go (표준 `net/http`, Go 1.22+ 메서드 라우팅), 포트 `:8080` — web-backend
- **클라이언트**: TypeScript + React (Vite), 브라우저 `fetch` / `WebSocket` — web-frontend
- **직렬화**: JSON (`Content-Type: application/json`), WS는 JSON 텍스트 프레임

## 의존 도구 · 시스템

- **SQLite** — `incidents` 테이블(상태·해제 attribution 영속). 내부 테이블·컬럼명은 `incident` 계열 유지.
- **JWT (HS256)** — `/api/incidents*` 접근 인증(user 조회, admin 해소). role: `admin` · `user` · `temp`(read-only).
- **WebSocket `/ws`** — 해소 브로드캐스트(`incident_resolved`) 소비.
- **하위 서비스**: recording(아카이브 finalize 비동기 트리거), hw-gateway(`/api/alert/resolved` 경유 MQTT 발행), 센서 펌웨어(물리 버튼 해소 → hw-gateway → `resolve-from-sensor`).

## 입력

내부 엔티티명·경로·WS 타입은 **불변**(리네임 대상 아님):

| Method | Path | Auth | 입력 |
|--------|------|------|------|
| GET | `/api/incidents` | user | query: `page`(≥1, def 1) · `limit`(def 20, max 100) · `from` · `to`(occurred_at, SQLite datetime) · `status`(**`open\|resolved`** — `acknowledged` 제거) |
| GET | `/api/incidents/active` | user | — (미해결 경보 배너 backfill 전용) |
| PATCH | `/api/incidents/{id}/resolve` | admin | `{resolutionNotes?: string}` — **선택**(생략·빈 문자열·공백 허용) |
| POST | `/api/incidents/{id}/resolve-from-sensor` | none (internal) | 센서 버튼 해소 — `interface-web-api.md` 계약 13 바디 그대로 |
| POST | `/api/incidents` | none (internal) | crisis 생성 — `interface-web-api.md` 계약 13 그대로(`open`으로 생성) |

**제거된 입력(계약에 존재하지 않음)**: `PATCH /api/incidents/{id}/acknowledge`. 확인 전이를 트리거하는 어떤 경로도 계약에 없다.

## 출력 (계약)

### `GET /api/incidents` → `200`

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

- `status`는 **`"open"` 또는 `"resolved"`만** 취한다 — 응답 어디에도 `"acknowledged"` 값이 나타나지 않는다.
- `resolutionNotes`는 `null` 또는 빈 문자열일 수 있다(해소 시 노트를 주지 않았거나 빈 노트로 해소한 경우).

### `GET /api/incidents/active` → `200`

미해결 경보 배열. **미해결 = `status == "open"`만**(resolved 제외; `acknowledged`는 존재하지 않음). 각 원소는 WS `crisis_alert` payload(계약 14)와 **동형**이다(배너 backfill이 실시간 push와 같은 모양을 재구성):

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

- 모든 원소의 `status == "open"`. 발생시각 내림차순. 식별자는 `incidentId`, 현장 연락정보는 `site`로 중첩.

### `PATCH /api/incidents/{id}/resolve` → `200`

```json
{ "status": "resolved", "resolvedByKind": "web", "resolvedById": "<username>", "resolvedByLabel": "..." }
```

- `open` 경보를 **선행 확인 없이** 직접 해소한다.
- `resolutionNotes`가 생략·빈 문자열·공백뿐이어도 **`400`이 아니다** — `200`으로 해소되며 노트는 빈 값(또는 `null`)으로 저장된다.
- 이미 `resolved`면 `409`(재해소 방어, 종단 상태).

### `POST /api/incidents/{id}/resolve-from-sensor` → `200`

센서 물리 버튼 해소. `interface-web-api.md` 계약 13의 출력 계약을 그대로 유지한다(`resolvedByKind == "sensor_button"`). `open → resolved`로 전이하며, 매칭되는 미해결(=`open`) 경보가 없으면 `404`, 명시적 id가 이미 resolved면 `409`.

### 사용자 대면 표면 (web-frontend)

- 경보 이력 탭/네비게이션/페이지 제목에 렌더되는 텍스트는 **"경보"**를 포함하고 **"사고"를 포함하지 않는다**.
- 해소된 경보 카드는 `resolvedByKind`/`resolvedByLabel`로 해제 주체를 표시한다(web=🖥, sensor_button=🔘) — 표기 어휘만 "경보"로 통일되며 attribution 표시는 유지된다.
- UI에 "확인(acknowledge)" 액션 버튼/전이는 노출되지 않는다 — 미해결 경보의 처리 액션은 "해소"뿐이다.

### API 계약 델타 (구현 중 `interface-web-api.md`에 반영할 정합화 — 본 문서는 interface-web-api.md를 편집하지 않음)

구현 세션은 아래를 `interface-web-api.md`(계약 2, 및 계약 13/14의 관련 문구)에 적용한다:

- **엔드포인트 제거**: `PATCH /api/incidents/{id}/acknowledge` 행·출력·단언을 계약 2에서 삭제.
- **enum 축소**: `status` 값 집합을 `{open, acknowledged, resolved}` → **`{open, resolved}`**. 계약 2의 `GET /api/incidents` `status` 쿼리, 응답 스키마 예시, `/api/incidents/active`의 `status ∈ {open, acknowledged}`를 **`{open}`**(active) / **`{open, resolved}`**(목록)로 정정.
- **상태 기계 문구 정정**: 계약 2 불변식 "`open → acknowledged → resolved`" → **"`open → resolved`(중간 확인 상태 없음)"**.
- **해제 노트 선택화**: 계약 2의 `PATCH .../resolve` 입력 `{resolutionNotes*: non-empty string}` → `{resolutionNotes?: string}`, 그리고 "notes 비어있으면 `400`" 및 대응 단언 삭제.
- 내부 엔티티명·경로·DB 테이블·WS 타입(`crisis_alert`/`incident_resolved`)은 **변경 금지** — 리네임은 web-frontend 표기 카피에 한정.

## 핵심 로직 (동작)

- **상태 기계**: `open → resolved` 단일 전이. `resolved`는 종단(재해소 `409`). 중간 상태 없음 — 시스템에 `acknowledged` 상태 값도, 그 전이를 유발하는 액션도 존재하지 않는다.
- **직접 해소**: `open` 경보는 선행 확인 없이 해소 가능. 확인 단계를 거치도록 강제하지 않는다.
- **해제 노트는 선택 메타데이터**: 노트 유무·공백 여부는 해소 성공/실패에 영향을 주지 않는다. 빈 노트로 해소해도 `resolved` 상태가 확정된다.
- **양방향 해소 attribution 유지**: `resolvedByKind ∈ {"web", "sensor_button", null}`. 웹 해제(본 계약 `resolve`)와 센서 버튼 해제(`resolve-from-sensor`)가 동일 필드에 기록된다.
- **해소 부작용 3종(보존)**: 해소 성공 시 — (a) recording 아카이브 finalize 비동기 트리거, (b) hw-gateway `/api/alert/resolved`(계약 15) 경유 MQTT `safety/{siteId}/alert/resolved` 발행, (c) WS `incident_resolved`(계약 14) 브로드캐스트. 웹 경로·센서 경로 모두 (a)(c)를 수반한다.
- **미해결 = `open`**: "미해결/active"의 정의는 `status == "open"`. `/api/incidents/active`와 배너 backfill, 그리고 센서 해소 폴백 체인의 "가장 최근 미해결 incident" 매칭은 모두 `open` 집합을 대상으로 한다.
- **표기 리네임의 경계**: "경보"로의 어휘 변경은 web-frontend가 렌더하는 사용자 대면 텍스트에 한정된다. API 응답 필드명, 경로, WS 메시지 타입, DB 스키마는 `incident` 어휘를 유지하여 계약·소비자 호환성을 깨지 않는다.

## 검증 단언 (TDD)

- **A (핵심)** — `open` 경보에 `PATCH /api/incidents/{id}/resolve`(유효 노트) → `200`, 응답 `status == "resolved"` · `resolvedByKind == "web"`. 선행 acknowledge 호출 없이 성공한다.
  ```bash
  curl -s -X PATCH -H "Authorization: Bearer $ADMIN" \
    http://localhost:8080/api/incidents/$ID/resolve \
    -d '{"resolutionNotes":"현장 확인 완료"}' | jq -e '.status=="resolved" and .resolvedByKind=="web"'
  ```
- **B (핵심)** — 빈/공백 노트로 해소 → `400`이 아니라 `200`, 저장 상태 `resolved`, 노트는 빈 값 허용. `""`·`"   "`·`resolutionNotes` 필드 생략 세 경우 모두 `200`.
  ```bash
  curl -s -o /dev/null -w '%{http_code}' -X PATCH -H "Authorization: Bearer $ADMIN" \
    http://localhost:8080/api/incidents/$ID/resolve -d '{"resolutionNotes":""}'   # → 200
  ```
- **C (핵심)** — 계약에 **acknowledge 전이가 없다**: `PATCH /api/incidents/{id}/acknowledge` 시도는 계약된 성공 응답(`200 {"status":"acknowledged"}`)을 반환하지 않는다(라우트 부재로 `404`/`405`). 그리고 어떤 `GET /api/incidents` 응답에도 `status == "acknowledged"`인 원소가 없다.
  ```bash
  curl -s -o /dev/null -w '%{http_code}' -X PATCH -H "Authorization: Bearer $ADMIN" \
    http://localhost:8080/api/incidents/$ID/acknowledge   # → 404 또는 405 (200 아님)
  ```
- **D (핵심)** — `GET /api/incidents/active` → `200`, 모든 원소 `status == "open"`(resolved·acknowledged 미포함), 각 원소에 `incidentId` · `site.address` · `site.managerName` · `site.managerPhone` 존재.
  ```bash
  curl -s -H "Authorization: Bearer $T" http://localhost:8080/api/incidents/active \
    | jq -e 'all(.[]; .status=="open" and has("incidentId") and (.site|has("address") and has("managerName") and has("managerPhone")))'
  ```
- **E** — `GET /api/incidents` 응답의 모든 `data[].status`가 `{open, resolved}` 안에 든다(`acknowledged` 부재).
  ```bash
  curl -s -H "Authorization: Bearer $T" 'http://localhost:8080/api/incidents?limit=100' \
    | jq -e 'all(.data[]; .status=="open" or .status=="resolved")'
  ```
- **F** — `GET /api/incidents?status=resolved` → `data[]`의 모든 `status == "resolved"`. `status=open` → 모든 `status == "open"`.
- **G (핵심)** — 같은 경보에 `resolve` 재호출 → `409`(resolved는 종단).
- **H (핵심)** — 해소 성공 직후 접속 중 WS 클라이언트가 `type == "incident_resolved"` 메시지를 수신하고 `payload.incidentId`가 해소한 경보의 id와 일치(계약 14 교차).
- **I (핵심)** — 센서 버튼 해소 경로 유지: `open` 경보에 대한 `POST /api/incidents/{id}/resolve-from-sensor`(유효 바디) → `200`, `resolvedByKind == "sensor_button"`, 이후 그 경보의 `status == "resolved"`. `open → resolved`로 직접 전이한다(중간 확인 없음).
- **J** — 해소 부작용 관측: 웹 해소 성공 시 (a) 해당 site로 hw-gateway `/api/alert/resolved` 발행(MQTT `safety/{siteId}/alert/resolved` 관측 가능) (c) WS `incident_resolved` 브로드캐스트가 각각 1회 발생. (아카이브 finalize (b)는 비동기 트리거이므로 recording 접면에서 별도 관측.)
- **K (핵심)** — 사용자 대면 표기: web-frontend의 경보 이력 탭/네비/페이지 제목 렌더 텍스트가 문자열 **"경보"**를 포함하고 **"사고"를 포함하지 않는다**(브라우저에서 관측 가능한 UI 텍스트 단언 — 예: 이력 탭 라벨의 textContent에 "경보" 포함, "사고" 미포함).
- **L** — user 토큰으로 `PATCH /api/incidents/{id}/resolve` → `403`(해소는 admin 전용; 조회 `GET /api/incidents`는 user `200`).

## 검증 스킵 선언 (선택)

- **K** — 사유: 브라우저 렌더 텍스트 단언으로 Playwright 세션이 필요(web-frontend needs-browser 계열). API 계층만으로는 판정 불가. · 중요도: **핵심(load-bearing)** (표기 리네임이 이 단위의 3대 보장 중 하나) · 해소 조건: Playwright 세션 실행 시 즉시 판정(INDEX.md SKIP 해제 조건 4와 동류).
