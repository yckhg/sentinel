# 카메라 변경 전파 · 삭제 증거 보존 스펙

> 상태: 살아있는 계약 (living spec) · 독자: 스펙 작성자 / 오케스트레이터
> 접합부: **web-backend → (cctv-adapter · youtube-adapter · recording)**, **web-backend · recording** (증거 저장소)
> 본 문서는 이미 존재하는 카메라 CRUD의 두 가지 계약을 **회귀가드로 고정**한다: (1) 카메라 변경이 녹화까지 전파되는 것, (2) 카메라 삭제가 사고·보호 아카이브 증거를 지우지 않는 것.
> Web API 접면의 전면 SSOT는 `docs/spec/interface-web-api.md`이며, 본 문서는 그 계약 중 "카메라 변경 전파·삭제 증거 보존" 단위의 의도를 규정한다. 충돌 시 본 단위 범위 안에서는 본 문서가 최신 의도를 가지며, 구현 중 `interface-web-api.md`·`web-backend.md`·`recording.md`를 정합화한다(아래 "델타" 참조).
> 센서 장치 생명주기(등록·삭제·재출현)는 별개 잎 `docs/spec/sensor-device-lifecycle.md`가 소유한다.

## 목적 / 의도

카메라 CRUD(추가·수정·삭제)는 이미 존재한다. 이 단위는 그 CRUD의 두 계약을 판정 가능한 회귀가드로 고정한다. **본 단위는 카메라 스키마·CRUD 자체를 바꾸지 않으며**, 편집 범위는 (a) web-backend에서 recording으로 가는 reload 팬아웃 배선 추가와 (b) 증거 보존 회귀 테스트에 한정한다.

1. **카메라 변경은 세 소비자 모두에 전파된다** — 카메라 생성·수정·삭제는 스트리밍 소비자(cctv-adapter·youtube-adapter)뿐 아니라 녹화(recording)에도 전파되어, 재시작 없이 반영된다(추가된 카메라 녹화 시작, 삭제된 카메라 녹화 중단). recording 소비자 측(reload 수신 핸들러 `POST /api/cameras/reload` · `GET /internal/cameras` 재조회 · 기동 재동기)은 **이미 구현되어 있다**; 본 잎의 델타는 web-backend가 recording에도 팬아웃을 보내도록 배선하는 것이다.
2. **카메라 삭제는 증거를 지우지 않는다** — 카메라 삭제는 그 카메라의 `stream_key`에 연관된 **보호·finalize된 녹화 아카이브**를 삭제하지 않으며, **사고(incident) 레코드**에도 어떤 영향을 주지 않는다. 교체(삭제 + 추가)로 인한 라이브 이력 표시 단절은 수용하되, 증거 자체는 보존되어 조회 가능하다.

배경 의도: 카메라 CRUD의 reload 팬아웃은 cctv-adapter·youtube-adapter에만 가고 recording에는 전파되지 않아, 카메라 변경이 재시작 전까지 녹화에 반영되지 않는 갭이 있었다. 또한 카메라를 삭제해도 증거가 보존됨은 현재 동작이나 계약으로 고정돼 있지 않아, 향후 reload 소비자(recording)의 재조정 로직이나 cleanup 변경이 삭제된 stream_key의 아카이브를 실수로 연쇄삭제(purge)할 위험이 있다. 산업안전 시스템에서 증거 보존은 필수 불변식이므로 이를 회귀가드로 못박는다.

## 언어 · 런타임

- **web-backend**: Go (표준 `net/http`, Go 1.22+ 메서드 라우팅), 포트 `:8080`
- **recording**: Go, 카메라 목록 소비자(reload 수신·재조정)이자 녹화 아카이브 저장소
- **직렬화**: JSON (`Content-Type: application/json`)

## 의존 도구 · 시스템

- **SQLite** — `cameras` 테이블(서버 발급 `stream_key` 불변). `incidents` 테이블(증거 중 사고 축).
- **녹화 아카이브** — recording 서비스가 소유. 아카이브 메타는 `ArchiveMetadata{IncidentID, StreamKey, FilePath, Status, …}`(JSON `metadata.json` 엔트리) + `FilePath`의 실제 세그먼트 파일로 표현된다. 보호(protect)·finalize는 recording의 2단계 상태 전이이며(`recording.md` SSOT), `Status=completed`(및 protecting/finalizing 계열)로 관측한다. **주의: protected 여부의 in-memory 플래그(`RecordingManager.protected` 맵)는 재기동 시 휘발하므로 판정 축으로 쓰지 않는다** — 판정은 `metadata.json` 엔트리 + 세그먼트 파일 실재로 한다.
- **JWT (HS256)** — 카메라 CRUD는 admin 전용.
- **내부 HTTP reload** — web-backend → `POST {cctv-adapter}/api/cameras/reload` · `POST {youtube-adapter}/api/cameras/reload` · `POST {recording}/api/cameras/reload`. 세 소비자 URL은 web-backend의 패키지 변수(`cctvAdapterURL`·`youtubeAdapterURL`·`recordingURL`)이며 각 환경변수(`CCTV_ADAPTER_URL`·`YOUTUBE_ADAPTER_URL`·`RECORDING_URL`)로 오버라이드된다(테스트에서 mock 수신 서버로 지정 가능). 소비자는 수신 시 `GET /internal/cameras`를 재조회해 실행 상태를 재조정(reconcile)한다. 이 internal reload/목록 경로는 내부 호출자만 접근하도록 강제한다.

## 카메라 ↔ 센서 · 사고 경계

- 카메라는 `cameras` 테이블만 사용한다 — `devices` 테이블·센서 자동발견·센서 삭제 모델(`sensor-device-lifecycle` 잎)의 대상이 아니다. 카메라 삭제는 카메라 CRUD(`DELETE /api/cameras/{id}`, 하드 삭제)로만 이뤄진다.
- **`incidents` 테이블은 카메라를 참조하지 않는다** — `incidents`에는 `stream_key`·`camera_id` 컬럼이 없고 `site_id`·`device_id`(센서)·`alert_id`(MQTT 알림)로만 키잉된다(현행 스키마 사실). 따라서 카메라 삭제는 사고 레코드에 **구조적으로 어떤 연쇄 경로도 없다**. 카메라↔증거 연결은 오직 **recording 아카이브의 `StreamKey`**(및 그 아카이브가 참조하는 `IncidentID`)로만 성립한다. 본 잎의 "증거 보존"은 이 **아카이브 축**이 핵심이며, 사고 축은 "카메라 삭제와 무관함(비참조)"의 회귀가드다.

---

## 계약 1 — 카메라 변경의 3소비자 전파 (web-backend → cctv-adapter · youtube-adapter · recording)

### 입력

| Method | Path | Auth | 입력 |
|--------|------|------|------|
| POST/PUT/DELETE | `/api/cameras` · `/api/cameras/{id}` | admin | 기존 카메라 CRUD (본 단위는 스키마 불변) |

### 출력 (계약)

- 카메라 생성·수정·삭제의 **DB 쓰기(Exec)가 에러 없이 성공한 뒤에만**, web-backend는 세 소비자 — cctv-adapter · youtube-adapter · **recording** — 각각에 `POST /api/cameras/reload`를 디스패치한다. 각 소비자는 이를 수신해 `GET /internal/cameras`를 재조회하고 실행 상태를 재조정한다. DB 쓰기가 실패(4xx/5xx, 예: 존재하지 않는 id 삭제로 rowsAffected==0)하면 디스패치하지 않는다.

### 핵심 로직 (동작)

- **DB 성공 후 전파** — reload 디스패치는 CRUD의 DB 쓰기 성공 이후에만 발사된다. (현행 CRUD는 명시적 `BEGIN/COMMIT` 없는 단일 `db.ExecContext`(SQLite autocommit)이며, "성공 후 발사"는 Exec 성공·에러 조기반환으로 이미 성립한다 — 명시적 트랜잭션을 새로 도입할 필요는 없다.) 쓰기 전 발사 시 소비자가 변경 전 상태를 재조회하는 순서 버그를 피한다.
- **3소비자 팬아웃** — 대상은 cctv-adapter · youtube-adapter · recording 셋이다. `triggerRecordingReload`를 create·update·delete **세 핸들러 모두**에 배선한다(한 곳이라도 누락되면 그 경로에서 recording은 재시작 전까지 변경을 반영하지 못한다).
- **최선노력·실패 내성** — 팬아웃은 비동기(각 소비자별 독립 goroutine·독립 client)이며 CRUD 응답은 팬아웃 완료를 기다리지 않는다. 한 소비자가 실패(다운·타임아웃)해도 나머지 디스패치는 진행하고 CRUD는 성공으로 응답한다.
- **재동기 안전망** — 소비자는 기동/재연결 시 스스로 `GET /internal/cameras`로 최신 카메라를 재조정한다(recording 측 이미 구현). reload 유실(소비자 일시 다운)은 이 재동기로 복구된다.

---

## 계약 2 — 카메라 삭제의 증거 보존 (web-backend · recording)

### 입력

| Method | Path | Auth | 입력 |
|--------|------|------|------|
| DELETE | `/api/cameras/{id}` | admin | — |

### 출력 (계약)

- 카메라 삭제는 `cameras` 레지스트리에서 그 카메라 행만 제거한다(`DELETE FROM cameras`). 삭제 후:
  - **(아카이브 축, 핵심)** 그 카메라의 `stream_key`로 키잉된 **보호·finalize(`Status=completed`)된 녹화 아카이브**는 보존된다 — `metadata.json` 엔트리와 `FilePath` 세그먼트 파일이 여전히 실재하며, 삭제된 `stream_key`로 `GET /api/archives`(또는 recording 아카이브 조회) 시 조회된다. reload 재조정으로 recording이 제거된 카메라의 recorder를 정지한 **이후에도** 보존된다.
  - **(사고 축)** 사고(incident) 레코드는 카메라를 참조하지 않으므로 삭제에 영향받지 않는다 — 삭제 전후로 `GET /api/incidents` 결과 집합이 불변이다(cameras 조인 없이 조회 성립).

### 핵심 로직 (동작)

- **비연쇄 삭제(구조적)** — `cameras` 행 삭제는 증거 저장소로 연쇄(cascade)하지 않는다. (a) 사고: `incidents`가 `cameras`를 FK로 참조하지 않으므로 연쇄 경로 자체가 없다(자명 비연쇄). (b) 아카이브: web-backend 카메라 삭제 핸들러(`DELETE FROM cameras`만 수행)는 recording 아카이브 파일/메타에 접근 경로가 없고, recording의 reload 재조정은 recorder 정지만 하며 보호·finalize된 아카이브를 purge하지 않는다.
- **능동 회귀가드** — 단순히 "연쇄가 없다"(현행 자명)를 넘어, 삭제된 `stream_key`로 아카이브가 **여전히 조회됨**을 능동 관측한다. 그래야 향후 누군가 reload 소비자나 cleanup에 "삭제 카메라 아카이브 purge"를 넣으면 즉시 NOK가 난다(미래 회귀가드로서의 실효).
- **이력 표시 단절 수용** — 교체 시 새 카메라는 새 `stream_key`를 받으므로 과거 이력이 새 카메라 행에 자동 연결되지 않는다. 이는 수용하며(증거 오염 방지), 증거 자체는 `stream_key`·`IncidentID`로 조회 가능하다. 자동 이력 재연결은 도입하지 않는다.
- **경계** — 보호되지 않은 롤링 세그먼트는 기존 롤링 윈도우 정책대로 자연 만료된다(삭제와 무관한 정상 동작). 본 계약은 **보호·finalize된 증거**의 보존만 규정한다.

## 검증 단언 (TDD)

- **A (핵심)** — 3소비자 성공-후 전파: 카메라 생성·수정·삭제 각각이 DB 쓰기에 성공한 뒤, cctv-adapter · youtube-adapter · recording 각각에 `POST /api/cameras/reload`가 **1회 이상 디스패치**된다(코얼레싱 허용 — 순서·정확 카운트 무관). **관측 프로토콜**: 세 소비자 URL을 수신 계측 서버로 지정하고, 각 계측기가 수신 요청을 카운트하며, CRUD 2xx 후 **세 계측기 각각의 카운트가 ≥1이 될 때까지 상한 T초(예 ≤5s) 폴링**한다(T 내 미도달이면 NOK — 비동기 fire-and-forget이므로 즉시 확인 금지). 실패 내성: 한 계측기를 닫힌 포트로 두어도 나머지 둘이 T 내 수신하고 CRUD는 2xx. DB 쓰기 실패(예: 없는 id DELETE) 시에는 어떤 계측기도 수신하지 않음.
  - **상시 게이트(in-process httptest)**: 세 소비자 URL이 env로 오버라이드되므로, Go 테스트에서 `httptest.Server` 3개(하나는 닫힘)를 URL로 지정하고 카메라 CRUD 핸들러를 in-process 호출해 팬아웃·실패내성을 **도커 스택 없이 상시 판정**한다. 이 in-process 버전이 A의 기본 게이트다(아래 SKIP 해소).
  ```go
  // web-backend 패키지 테스트: cctv/youtube/recordingURL을 httptest.Server 3개로 지정.
  // create/update/delete 각각 → 세 계측기 count≥1을 ≤5s 폴링. 하나 닫아도 나머지 수신 + 2xx.
  ```
- **B1 (핵심)** — 삭제 사고 축 불변: 사고 레코드가 존재하는 상태에서 어떤 카메라를 `DELETE /api/cameras/{id}` → 삭제 전후 `GET /api/incidents` 결과 집합이 불변(사고는 카메라 비참조이므로 삭제 영향 없음).
- **B1s (핵심, static — 비-mutating)** — 사고 비참조(정적): `incidents` 스키마·삭제 경로에 `cameras` FK·`ON DELETE CASCADE`가 없고, 카메라 DELETE 핸들러(`cameras.go` handleDeleteCamera)에 `incidents`/아카이브 테이블에 대한 DELETE 문이 없음을 정적 스캔으로 판정(형제 잎 H2와 동형의 값싼 상시 게이트).
- **B2 (핵심, mutating)** — 삭제 아카이브 증거 보존: 어떤 카메라의 `stream_key`로 키잉된 **보호·finalize(`Status=completed`)된 아카이브**가 존재하는 상태(픽스처: 세그먼트 시딩 → `POST /api/archives/protect` → `POST /api/archives/finalize` → `Status=completed` 확인)에서 그 카메라를 `DELETE /api/cameras/{id}` → recording이 reload 재조정으로 그 stream_key recorder를 정지한 **이후**에도, (a) 삭제된 `stream_key`로 그 아카이브가 조회되고(`GET /api/archives` 또는 recording 아카이브 조회), (b) `metadata.json` 엔트리와 `FilePath` 세그먼트 파일이 파일시스템에 실재한다. protect 여부의 in-memory 플래그가 아니라 메타·파일 실재로 판정한다.

## 검증 스킵 선언 (선택)

- **A** — **기본 게이트는 in-process httptest로 상시 판정**(env-override URL로 mock 3개 지정, 격리 스택 불필요). 따라서 A는 load-bearing SKIP이 **아니다**. 다중서비스 격리 스택에서의 end-to-end 재확인(recording 실제 reconcile까지)은 보조로 두되, 기본 판정은 상시 게이트로 성립한다.
- **B2** — 사유: mutating·다중서비스 — protected·finalize 아카이브 픽스처를 세우고 recording reconcile 이후 파일·메타 보존을 관측하려면 recording 볼륨/아카이브가 있는 격리 스택이 필요하다(픽스처 절차: 세그먼트 시딩 → protect → finalize → completed 확인 → 카메라 DELETE → reconcile 대기 → 조회, SSOT는 `recording.md`). · 중요도: **핵심(load-bearing)** (산업안전 증거 보존이 이 단위의 핵심) · 해소 조건: recording 포함 격리 스택 + 사고/아카이브 픽스처. (B1·B1s는 web-backend + 정적 스캔으로 상시 판정 가능.)

## 델타 (SSOT 정합 — 오케스트레이터 머지 반영분)

> **편집 경계 (SSOT 위임)**: 본 잎은 **코드(web-backend `cameras.go`에 recording 트리거 배선) + 자기 `tests/` + 본 스펙 문서**를 편집한다. recording 소비자 측은 이미 완비되어 재작업하지 않는다. 아래 인터페이스/서비스 SSOT 정합은 오케스트레이터가 머지 시점에 반영한다.

- **`interface-web-api.md`**: 카메라 CRUD의 reload 팬아웃 소비자 목록에 **recording**을 추가하고, 팬아웃이 DB 쓰기 성공 후·비동기·최선노력임을 명시. 카메라 삭제가 사고(비참조)·보호 아카이브(stream_key 키잉)를 보존함을 명시.
- **`web-backend.md`**: 카메라 CRUD가 recording을 포함한 3소비자에 성공-후 reload를 디스패치함을 반영(`triggerRecordingReload`를 3핸들러에 배선). 카메라 삭제의 증거 비연쇄(사고=구조적 비참조, 아카이브=recording 소유·미purge)를 명시.
- **`recording.md`**: `POST /api/cameras/reload` 수신 시 카메라 목록 재조정을 카메라 CRUD 전파의 정식 소비자로 반영(이미 구현된 reload 수신·reconcile·기동 재동기를 계약으로 고정). reload 재조정의 recorder 정지가 보호·finalize 아카이브를 purge하지 않음을 증거 보존 불변식으로 명시.
