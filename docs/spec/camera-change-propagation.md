# 카메라 변경 전파 · 삭제 증거 보존 스펙

> 상태: 살아있는 계약 (living spec) · 독자: 스펙 작성자 / 오케스트레이터
> 접합부: **web-backend → (cctv-adapter · youtube-adapter · recording)**, **web-backend · recording** (증거 저장소)
> 본 문서는 이미 존재하는 카메라 CRUD의 두 가지 계약을 **회귀가드로 고정**한다: (1) 카메라 변경이 녹화까지 전파되는 것, (2) 카메라 삭제가 사고·보호 아카이브 증거를 지우지 않는 것.
> Web API 접면의 전면 SSOT는 `docs/spec/interface-web-api.md`이며, 본 문서는 그 계약 중 "카메라 변경 전파·삭제 증거 보존" 단위의 의도를 규정한다. 충돌 시 본 단위 범위 안에서는 본 문서가 최신 의도를 가지며, 구현 중 `interface-web-api.md`·`web-backend.md`·`recording.md`를 정합화한다(아래 "델타" 참조).
> 센서 장치 생명주기(등록·삭제·재출현)는 별개 잎 `docs/spec/sensor-device-lifecycle.md`가 소유한다.

## 목적 / 의도

카메라 CRUD(추가·수정·삭제)는 이미 존재한다. 이 단위는 그 CRUD의 두 계약을 판정 가능한 회귀가드로 고정한다.

1. **카메라 변경은 세 소비자 모두에 전파된다** — 카메라 생성·수정·삭제는 스트리밍 소비자(cctv-adapter·youtube-adapter)뿐 아니라 **녹화(recording)에도 전파**되어, 재시작 없이 반영된다(추가된 카메라 녹화 시작, 삭제된 카메라 녹화 중단).
2. **카메라 삭제는 증거를 지우지 않는다** — 카메라 삭제는 그 카메라의 `stream_key`에 연관된 **보호·finalize된 아카이브**와 **사고(incident) 레코드**를 삭제하지 않는다. 교체(삭제 + 추가)로 인한 라이브 이력 표시 단절은 수용하되, 증거 자체는 보존되어 사고번호·발생일시·`stream_key`로 조회 가능하다.

배경 의도: 카메라 CRUD의 reload 팬아웃은 cctv-adapter·youtube-adapter에만 가고 recording에는 전파되지 않아, 카메라 변경이 재시작 전까지 녹화에 반영되지 않는 갭이 있었다. 또한 카메라를 삭제해도 증거가 보존됨은 현재 동작이나 계약으로 고정돼 있지 않아, 향후 cleanup 변경이 증거를 실수로 연쇄삭제할 위험이 있다. 산업안전 시스템에서 증거 보존은 필수 불변식이므로 이를 회귀가드로 못박는다. **본 단위는 카메라 스키마·CRUD 자체를 바꾸지 않는다.**

## 언어 · 런타임

- **web-backend**: Go (표준 `net/http`, Go 1.22+ 메서드 라우팅), 포트 `:8080`
- **recording**: Go, 카메라 목록 소비자(reload 수신)
- **직렬화**: JSON (`Content-Type: application/json`)

## 의존 도구 · 시스템

- **SQLite** — `cameras` 테이블(서버 발급 `stream_key` 불변), `incidents` 테이블·아카이브 메타(증거).
- **JWT (HS256)** — 카메라 CRUD는 admin 전용.
- **내부 HTTP reload** — web-backend → `POST {cctv-adapter}/api/cameras/reload` · `POST {youtube-adapter}/api/cameras/reload` · `POST {recording}/api/cameras/reload`. 소비자는 수신 시 `GET /internal/cameras`를 재조회해 실행 상태를 재조정(reconcile)한다. 이 internal reload/목록 경로는 내부 호출자만 접근하도록 강제한다.

## 카메라 ↔ 센서 경계

- 카메라는 `cameras` 테이블만 사용한다 — `devices` 테이블·센서 자동발견·센서 삭제 모델(`sensor-device-lifecycle` 잎)의 대상이 **아니다**. 카메라 삭제는 카메라 CRUD(`DELETE /api/cameras/{id}`, 하드 삭제)로만 이뤄진다.

---

## 계약 1 — 카메라 변경의 3소비자 전파 (web-backend → cctv-adapter · youtube-adapter · recording)

### 입력

| Method | Path | Auth | 입력 |
|--------|------|------|------|
| POST/PUT/DELETE | `/api/cameras` · `/api/cameras/{id}` | admin | 기존 카메라 CRUD (본 단위는 스키마 불변) |

### 출력 (계약)

- 카메라 생성·수정·삭제가 **DB 트랜잭션 커밋에 성공한 뒤**, web-backend는 세 소비자 — cctv-adapter · youtube-adapter · **recording** — 각각에 `POST /api/cameras/reload`를 디스패치한다. 각 소비자는 이를 수신해 `GET /internal/cameras`를 재조회하고 실행 상태를 재조정한다.

### 핵심 로직 (동작)

- **커밋 후 전파** — reload 디스패치는 CRUD 트랜잭션 커밋 성공 이후에만 발사된다. 커밋 전 발사 시 소비자가 변경 전 상태를 재조회하는 순서 버그를 피한다.
- **3소비자 팬아웃** — 대상은 cctv-adapter · youtube-adapter · recording 셋이다. 어느 하나라도 누락되면 그 소비자는 재시작 전까지 변경을 반영하지 못한다.
- **최선노력·실패 내성** — 팬아웃은 비동기이며 CRUD 응답은 팬아웃 완료를 기다리지 않는다. 한 소비자가 실패해도 나머지 디스패치는 진행하고 CRUD는 성공으로 응답한다.
- **재동기 안전망** — 소비자는 기동/재연결 시 스스로 `GET /internal/cameras`로 최신 카메라를 재조정한다. reload 유실(소비자 일시 다운)은 이 재동기로 복구된다.

---

## 계약 2 — 카메라 삭제의 증거 보존 (web-backend · recording)

### 입력

| Method | Path | Auth | 입력 |
|--------|------|------|------|
| DELETE | `/api/cameras/{id}` | admin | — |

### 출력 (계약)

- 카메라 삭제는 `cameras` 레지스트리에서 그 카메라 행만 제거한다. 그 카메라의 `stream_key`에 연관된 **보호(protected)·finalize된 아카이브**와 **사고(incident) 레코드**는 보존된다. 삭제 후에도 사고 레코드는 `GET /api/incidents`로 조회되고, 보호 아카이브 증거는 존재한다.

### 핵심 로직 (동작)

- **비연쇄 삭제** — `cameras` 행 삭제는 증거 저장소(사고 테이블·보호 아카이브)로 연쇄(cascade)하지 않는다. 사고·아카이브는 `cameras`를 외래키로 참조하지 않고, `stream_key` 문자열 값과 발생일시를 자기 행에 비정규화 보존한다 — 증거 조회는 `cameras` 조인 없이 성립한다.
- **이력 표시 단절 수용** — 교체 시 새 카메라는 새 `stream_key`를 받으므로 과거 이력이 새 카메라 행에 자동 연결되지 않는다. 이는 수용하며, 증거 자체는 사고번호·발생일시·`stream_key`로 조회 가능하다.
- **경계** — 보호되지 않은 롤링 세그먼트는 기존 롤링 윈도우 정책대로 자연 만료된다(삭제와 무관한 정상 동작). 본 계약은 **보호·finalize된 증거**의 보존만 규정한다.

## 검증 단언 (TDD)

- **A (핵심, mutating)** — 3소비자 커밋-후 전파: 카메라 생성·수정·삭제 각각이 커밋에 성공한 뒤, cctv-adapter · youtube-adapter · recording 각각에 `POST /api/cameras/reload`가 **1회 이상 디스패치**된다(코얼레싱 허용 — 순서·정확 카운트 무관, 각 소비자에 최소 1회 도달을 관측). 검증은 세 소비자 자리에 reload 수신 계측기(mock 수신 서버)를 두고, 카메라 CRUD 성공 후 세 계측기 모두가 요청을 수신함을 확인한다.
  ```bash
  # 격리 스택: cctv/youtube/recording 자리에 reload 수신 계측 서버 3개.
  # 카메라 1건 POST(커밋 성공) → 세 계측기 각각 /api/cameras/reload 1회+ 수신 관측.
  # 소비자 1개를 죽여도(실패 내성) 나머지 2개는 수신, CRUD는 2xx.
  ```
- **B (핵심, mutating)** — 삭제 증거 보존: 어떤 카메라의 `stream_key`에 연관된 사고 레코드와 **보호(protected) 아카이브**가 존재하는 상태에서 그 카메라를 `DELETE /api/cameras/{id}` → 삭제 후에도 (a) 그 사고 레코드가 `GET /api/incidents`로 (cameras 조인 없이) 조회되고, (b) 보호 아카이브가 여전히 존재한다(연쇄 제거되지 않음). 조회는 사고번호·발생일시·`stream_key` 값으로 성립한다.

## 검증 스킵 선언 (선택)

- **A** — 사유: mutating·다중서비스 — 카메라 CRUD의 3소비자 팬아웃을 관측하려면 세 소비자 자리에 reload 수신 계측기가 있는 격리 스택이 필요하다. · 중요도: **핵심(load-bearing)** · 해소 조건: reload 수신 계측기 3개를 둔 격리 스택.
- **B** — 사유: mutating·다중서비스 — 사고+보호 아카이브 시딩 후 카메라 삭제의 증거 보존을 관측하려면 recording 볼륨/아카이브가 있는 격리 스택이 필요하다. · 중요도: **핵심(load-bearing)** (산업안전 증거 보존이 이 단위의 핵심) · 해소 조건: recording 포함 격리 스택 + 사고/아카이브 픽스처.

## 델타 (SSOT 정합 — 오케스트레이터 머지 반영분)

> **편집 경계 (SSOT 위임)**: 본 잎은 **코드 + 자기 `tests/` + 본 스펙 문서**를 편집한다. 아래 인터페이스/서비스 SSOT 정합은 오케스트레이터가 머지 시점에 반영한다.

- **`interface-web-api.md`**: 카메라 CRUD의 reload 팬아웃 소비자 목록에 **recording**을 추가하고, 팬아웃이 커밋 후·최선노력임을 명시.
- **`web-backend.md`**: 카메라 CRUD가 recording을 포함한 3소비자에 커밋 후 reload를 디스패치함을 반영. 카메라 삭제의 증거 비연쇄(사고·보호 아카이브 미삭제, 증거는 `stream_key` 비정규화 보존) 가드를 명시.
- **`recording.md`**: `POST /api/cameras/reload` 수신 시 카메라 목록 재조정을 카메라 CRUD 전파의 정식 소비자로 반영(기존 시작-시 1회 조회 + reload 수신 소비자로 승격). 기동/재연결 시 `GET /internal/cameras` 재동기 안전망 명시.
