# 카메라 변경 전파 · 삭제 증거 보존 스펙

> 상태: 살아있는 계약 (living spec) · 독자: 스펙 작성자 / 오케스트레이터
> 접합부: **web-backend → (cctv-adapter · youtube-adapter · recording)**, **web-backend · recording** (증거 저장소)
> 본 문서는 이미 존재하는 카메라 CRUD의 두 가지 계약을 **회귀가드로 고정**한다: (1) 카메라 변경이 녹화까지 전파되는 것, (2) 카메라 삭제가 사고·보호 아카이브 증거를 지우지 않는 것.
> Web API 접면의 전면 SSOT는 `docs/spec/interface-web-api.md`이며, 본 문서는 그 계약 중 "카메라 변경 전파·삭제 증거 보존" 단위의 의도를 규정한다. 충돌 시 본 단위 범위 안에서는 본 문서가 최신 의도를 가지며, 구현 중 `interface-web-api.md`·`web-backend.md`·`recording.md`를 정합화한다(아래 "델타" 참조).
> 센서 장치 생명주기(등록·삭제·재출현)는 별개 잎 `docs/spec/sensor-device-lifecycle.md`가 소유한다.

## 목적 / 의도

카메라 CRUD(추가·수정·삭제)는 이미 존재한다. 이 단위는 그 CRUD의 두 계약을 판정 가능한 회귀가드로 고정한다. 본 잎은 기존 카메라 스키마·CRUD를 그대로 유지하고, 편집을 (a) web-backend에서 recording으로 가는 reload 팬아웃 배선 추가와 (b) 증거 보존 회귀 테스트로 한정한다.

1. **카메라 변경은 세 소비자 모두에 전파된다** — 카메라 생성·수정·삭제는 스트리밍 소비자(cctv-adapter·youtube-adapter)뿐 아니라 녹화(recording)에도 전파되어 재시작 없이 반영된다(추가 카메라 녹화 시작, 삭제 카메라 녹화 중단). recording 소비자 측(reload 수신 핸들러 `POST /api/cameras/reload` · `GET /internal/cameras` 재조회 · 기동 재동기)을 구현·검증 완료로 간주하고, 본 잎은 web-backend 팬아웃 배선(`triggerRecordingReload`)만 추가한다.
2. **카메라 삭제는 증거를 지우지 않는다** — 카메라 삭제는 그 카메라의 `stream_key`로 키잉된 **보호·finalize된 녹화 아카이브(병합 MP4 + 메타)**를 삭제하지 않으며, **사고(incident) 레코드**에도 어떤 영향을 주지 않는다. 교체(삭제 + 추가)로 인한 라이브 이력 표시 단절은 수용하되, 증거 자체는 보존되어 조회 가능하다.

배경 의도: 카메라 CRUD의 reload 팬아웃은 cctv-adapter·youtube-adapter에만 가고 recording에는 전파되지 않아, 카메라 변경이 재시작 전까지 녹화에 반영되지 않는 갭이 있었다. 또한 카메라를 삭제해도 증거가 보존됨은 현재 동작이나 계약으로 고정돼 있지 않아, 향후 reload 소비자(recording)의 재조정 로직이나 cleanup 변경이 삭제된 stream_key의 아카이브를 실수로 연쇄삭제(purge)할 위험이 있다. 산업안전 시스템에서 증거 보존은 필수 불변식이므로 이를 회귀가드로 못박는다.

## 언어 · 런타임

- **web-backend**: Go (표준 `net/http`, Go 1.22+ 메서드 라우팅), 포트 `:8080`
- **recording**: Go, 카메라 목록 소비자(reload 수신·재조정)이자 녹화 아카이브 저장소
- **직렬화**: JSON (`Content-Type: application/json`)

## 의존 도구 · 시스템

- **SQLite** — `cameras` 테이블(서버 발급 `stream_key` 불변). `incidents` 테이블(사고 레코드).
- **녹화 아카이브** — recording 서비스가 소유. 아카이브 메타는 `ArchiveMetadata{ID, IncidentID, StreamKey, FilePath, Status, CompletedAt, …}`(JSON `metadata.json` 엔트리)로 표현된다. 보호(protect)·finalize는 recording의 상태 전이이며(`recording.md` SSOT), finalize 성공 시 `Status=completed`가 되고 `FilePath`에 **병합 MP4**(`{ARCHIVES_DIR}/{archiveId}/{streamKey}.mp4`)가 원자적으로 기록된다. **판정 아티팩트는 이 병합 MP4 + `metadata.json` 엔트리다** — 원본 `.ts` 세그먼트는 finalize 후 보호 해제되어 롤링 클린업 대상이 되므로(정상 만료) 판정 대상이 아니다. in-memory `protected` 맵은 재기동 시 휘발하므로 역시 판정 축이 아니다.
- **JWT (HS256)** — 카메라 CRUD는 admin 전용.
- **내부 HTTP reload** — web-backend → `POST {cctv-adapter}/api/cameras/reload` · `POST {youtube-adapter}/api/cameras/reload` · `POST {recording}/api/cameras/reload`. 세 소비자 URL은 web-backend의 패키지 변수(`cctvAdapterURL`·`youtubeAdapterURL`·`recordingURL`)이며 각 환경변수(`CCTV_ADAPTER_URL`·`YOUTUBE_ADAPTER_URL`·`RECORDING_URL`)로 오버라이드된다(테스트에서 mock 수신 서버로 지정). 소비자는 수신 시 `GET /internal/cameras`를 재조회해 실행 상태를 재조정(reconcile)한다.

## 카메라 ↔ 센서 · 사고 경계

- 카메라는 `cameras` 테이블만 사용한다 — `devices` 테이블·센서 자동발견·센서 삭제 모델(`sensor-device-lifecycle` 잎)의 대상이 아니다. 카메라 삭제는 `DELETE /api/cameras/{id}`(하드 삭제)로만 이뤄진다.
- **`incidents` 테이블은 카메라를 참조하지 않는다** — `incidents`에는 `stream_key`·`camera_id` 컬럼이 없고 `site_id`·`device_id`(센서)·`alert_id`(MQTT 알림)로만 키잉된다(현행 스키마 사실). 따라서 카메라 삭제는 사고 레코드에 **구조적으로 어떤 연쇄 경로도 없다**. 카메라↔증거 연결은 오직 **recording 아카이브의 `StreamKey`**(및 그 아카이브가 참조하는 `IncidentID`)로만 성립한다. 본 잎의 "증거 보존"은 이 **아카이브 축**이 핵심이며, 사고 축은 "카메라 삭제와 무관함(비참조)"의 정적 회귀가드다.

---

## 계약 1 — 카메라 변경의 3소비자 전파 (web-backend → cctv-adapter · youtube-adapter · recording)

### 입력

| Method | Path | Auth | 입력 |
|--------|------|------|------|
| POST/PUT/DELETE | `/api/cameras` · `/api/cameras/{id}` | admin | 기존 카메라 CRUD (본 단위는 스키마 불변) |

### 출력 (계약)

- 카메라 생성·수정·삭제의 **DB 쓰기(Exec)가 에러 없이 성공한 뒤에만**, web-backend는 세 소비자 — cctv-adapter · youtube-adapter · **recording** — 각각에 `POST /api/cameras/reload`를 디스패치한다. 각 소비자는 수신해 `GET /internal/cameras`를 재조회하고 재조정한다. DB 쓰기가 실패(4xx/5xx, 예: 없는 id 삭제로 rowsAffected==0 → 404)하면 디스패치하지 않는다.

### 핵심 로직 (동작)

- **DB 성공 후 전파** — 기존 단일 `db.ExecContext`(SQLite autocommit)의 에러 조기반환이 "성공 후 발사"를 이미 보장하므로 그 구조를 유지한다(명시적 트랜잭션 신규 도입 불필요). 쓰기 전 발사 시 소비자가 변경 전 상태를 재조회하는 순서 버그를 피한다. A는 web-backend의 **팬아웃 행동**을 관측한다(recording의 실제 녹화 반영 결과는 `recording.md`가 소유).
- **3소비자 팬아웃** — `triggerRecordingReload`를 create·update·delete **세 핸들러 모두**에 기존 `triggerCCTVReload`/`triggerYouTubeReload`와 대칭으로 배선한다(성공 가드 이후 발사).
- **최선노력·실패 내성** — 팬아웃은 비동기(각 소비자별 독립 goroutine·독립 client)이며 CRUD 응답은 완료를 기다리지 않는다. 한 소비자가 실패해도 나머지는 진행하고 CRUD는 성공 응답.
- **재동기 안전망** — 소비자는 기동/재연결 시 스스로 `GET /internal/cameras`로 재조정한다(recording 측 이미 구현). reload 유실은 이 재동기로 복구된다.

---

## 계약 2 — 카메라 삭제의 증거 보존 (web-backend · recording)

### 입력

| Method | Path | Auth | 입력 |
|--------|------|------|------|
| DELETE | `/api/cameras/{id}` | admin | — |

### 출력 (계약)

- 카메라 삭제는 `cameras` 레지스트리에서 그 카메라 행만 제거한다(`DELETE FROM cameras`). 삭제 후:
  - **(아카이브 축, 핵심)** 그 카메라의 `stream_key`로 키잉된 **보호·finalize(`Status=completed`)된 아카이브**는 보존된다 — `metadata.json` 엔트리와 `FilePath`가 가리키는 **병합 MP4 파일**이 여전히 실재하며, 삭제된 `stream_key`로 아카이브 목록 조회 시 그 엔트리가 남아 있다. recording이 reload 재조정으로 그 stream_key의 recorder를 정지한(있었다면) **이후에도** 보존된다.
  - **(사고 축)** 사고(incident) 레코드는 카메라를 참조하지 않으므로 삭제에 영향받지 않는다 — 삭제 전후로 `GET /api/incidents` 결과 집합이 불변(cameras 조인 없이 조회 성립).

### 핵심 로직 (동작)

- **비연쇄 삭제(구조적)** — `cameras` 행 삭제는 증거 저장소로 연쇄하지 않는다. (a) 사고: `incidents`가 `cameras`를 FK로 참조하지 않으므로 연쇄 경로 자체가 없다. (b) 아카이브: web-backend 카메라 삭제 핸들러(`DELETE FROM cameras`만)는 recording 아카이브 파일/메타에 접근 경로가 없고, recording의 reload 재조정은 recorder 정지(stopCh·SIGTERM·states 삭제)만 수행하며 `archiveManager`·`metadata.json`·아카이브 파일에 접근하지 않는다(purge 없음).
- **능동 회귀가드** — 단순히 "연쇄가 없다"(현행 자명)를 넘어, 삭제된 `stream_key`로 아카이브가 **여전히 조회됨**을 능동 관측한다. 그래야 향후 누군가 reload 소비자나 cleanup에 "삭제 카메라 아카이브 purge"를 넣으면 즉시 NOK가 난다(미래 회귀가드).
- **이력 표시 단절 수용** — 교체 시 새 카메라는 새 `stream_key`를 받으므로 이력 조회는 `stream_key`·`IncidentID` 축으로만 성립시키고, 새 카메라 행으로의 자동 재연결은 범위 밖으로 둔다(증거 오염 방지).
- **경계** — 보호되지 않은 롤링 세그먼트는 기존 롤링 윈도우 정책대로 자연 만료된다(삭제와 무관). 본 계약은 **보호·finalize된 증거(MP4)**의 보존만 규정한다.

## 검증 단언 (TDD)

- **A (핵심, 상시 in-process 게이트)** — 3소비자 성공-후 전파: 카메라 생성·수정·삭제 각각이 DB 쓰기에 성공한 뒤, cctv-adapter · youtube-adapter · recording 각각에 `POST /api/cameras/reload`가 **1회 이상 디스패치**된다(코얼레싱 허용 — 순서·정확 카운트 무관). **판정은 in-process httptest**: 세 소비자 URL(`cctvAdapterURL`·`youtubeAdapterURL`·`recordingURL` 패키지 변수)을 `httptest.Server`로 지정(테스트 후 `t.Cleanup`으로 원복)하고, admin 컨텍스트를 주입해(기존 `adminReq`/`context.WithValue(AuthUser{Role:"admin"})` 패턴) 카메라 CRUD 핸들러를 호출한다. 팬아웃이 비동기 fire-and-forget이므로 **각 계측기 카운트 ≥1이 될 때까지 상한 T초(예 ≤5s) 폴링**(즉시 확인 금지). 실패 내성: 세 계측기 중 하나를 **닫힌(Closed) `httptest.Server`**(연결 거부)로 두어도 나머지 둘이 T 내 수신 + CRUD 2xx(지연 응답 서버 금지 — 닫힌 서버로 고정). DB 쓰기 실패(없는 id DELETE → 404) 시 어떤 계측기도 미수신. update/delete는 대상 카메라 행을 사전 시딩(미존재 시 404가 "실패=미디스패치"와 혼동되지 않도록).
  ```go
  // 세 URL을 httptest.Server 3개로 지정(하나는 Close()). create/update/delete 각각 →
  // 세 계측기 count≥1을 ≤5s 폴링. 닫힌 서버여도 나머지 수신 + 2xx. 없는 id DELETE → 미수신.
  ```
- **B1s (핵심, static — 비-mutating)** — 사고 비참조: `incidents` 스키마에 `cameras` FK·`ON DELETE CASCADE`·`camera_id`/`stream_key` 컬럼이 없고, 카메라 DELETE 핸들러(`handleDeleteCamera`)에 `incidents`/아카이브 테이블에 대한 DELETE 문이 없음을 정적 스캔으로 판정(형제 잎 H2와 동형의 값싼 상시 게이트). 사고 축의 실질 위험은 구조적 비참조라 이 정적 게이트로 충분하다.
- **B2 (핵심, mutating)** — 삭제 아카이브 증거 보존: 어떤 카메라의 `stream_key`로 키잉된 **보호·finalize(`Status=completed`)된 아카이브**가 존재하는 상태에서 그 카메라를 `DELETE /api/cameras/{id}` → recording reconcile 이후에도, (a) 삭제된 `stream_key`로 그 아카이브 엔트리가 조회되고, (b) `metadata.json` 엔트리와 `FilePath`의 **병합 MP4 파일**이 파일시스템에 실재한다. **부수 확인(사고 축)**: 같은 삭제 전후로 `GET /api/incidents` 결과 집합 불변.
  - **픽스처**: recording에 **유효 MPEG-TS 세그먼트**(실 RTMP 캡처 또는 사전 인코딩본, 파일명 `YYYYMMDD_HHMMSS.ts`)를 시딩하되 타임스탬프를 `[incidentTime-1h, resolvedAt+30min]` 창 안에 둔다 → `POST /api/archives/protect`(incidentTime) → `POST /api/archives/finalize`(resolvedAt) → 아카이브 목록을 폴링해 `Status=="completed" && sizeBytes>0` 확인(창 밖 세그먼트면 `failed`가 되므로 능동 확인). SSOT는 `recording.md`.
  - **관측 시점**: 카메라 DELETE 후 recording reconcile 완료는 `GET /api/status`(recording)에서 삭제된 stream_key의 recorder가 사라질 때까지 상한 T초 폴링으로 관측한 뒤 아카이브 잔존을 판정한다. (자동 reconcile은 계약 1의 recording 팬아웃 배선을 전제하며, 미배선 환경에서는 테스트가 recording에 직접 `POST /api/cameras/reload`를 발사한다.) 아카이브 조회는 `GET /api/archives` 응답 배열에서 `streamKey == 삭제된 키`인 항목 존재로 판정(엔드포인트에 stream_key 쿼리 파라미터는 없으므로 클라이언트측 필터). 라이브 recorder가 없는 픽스처면 "정지될 recorder"가 없어도 되며(불변식은 recorder 유무와 무관), 핵심은 DELETE·reconcile 후 아카이브(MP4+메타) 잔존이다.

## 검증 스킵 선언 (선택)

- **A** — **상시 in-process httptest 게이트로 판정**(env-override URL로 mock 3개, 격리 스택 불필요)이 기본이며 **load-bearing SKIP이 아니다**. 다중서비스 격리 스택에서 recording 실제 reconcile까지 보는 end-to-end는 **선택적 통합 스모크**(게이트 아님, 검증 부채로 카운트하지 않음)로만 둔다.
- **B2** — 사유: mutating·다중서비스 — 유효 미디어 세그먼트 시딩 → protect → finalize → completed 확인 → 카메라 삭제 → reconcile 관측 → 아카이브 조회를 하려면 recording 볼륨/아카이브가 있는 격리 스택이 필요하다. · 중요도: **핵심(load-bearing)** (산업안전 증거 보존이 이 단위의 핵심) · 해소 조건: recording 포함 격리 스택 + 유효 미디어/아카이브 픽스처. (A·B1s는 in-process + 정적 스캔으로 상시 판정 가능.)

## 델타 (SSOT 정합 — 오케스트레이터 머지 반영분)

> **편집 경계 (SSOT 위임)**: 본 잎은 **코드(web-backend `cameras.go`에 `triggerRecordingReload` 배선) + 자기 `tests/` + 본 스펙 문서**를 편집한다. recording 소비자 측(reload 수신·reconcile·기동 재동기)은 구현·검증 완료로 간주하고 재작업하지 않는다. 아래 인터페이스/서비스 SSOT 정합은 오케스트레이터가 머지 시점에 반영한다(형제 센서 잎도 `interface-web-api.md`·`web-backend.md`를 편집하므로 **오케스트레이터가 두 잎 델타를 직렬 반영**한다 — 카메라 잎은 cameras/reload 절, 센서 잎은 devices 절로 소유 분리).

- **`interface-web-api.md`**: 카메라 CRUD의 reload 팬아웃 소비자 목록에 **recording**을 추가하고, 팬아웃이 DB 쓰기 성공 후·비동기·최선노력임을 명시. 카메라 삭제가 사고(비참조)·보호 아카이브(stream_key 키잉 병합 MP4)를 보존함을 명시.
- **`web-backend.md`**: 카메라 CRUD가 recording을 포함한 3소비자에 성공-후 reload를 디스패치함을 반영(`triggerRecordingReload`를 3핸들러 배선). 카메라 삭제의 증거 비연쇄(사고=구조적 비참조, 아카이브=recording 소유·미purge)를 명시.
- **`recording.md`**: `POST /api/cameras/reload` 수신 시 카메라 목록 재조정을 카메라 CRUD 전파의 정식 소비자로 반영(이미 구현된 reload 수신·reconcile·기동 재동기를 계약으로 고정). reload 재조정의 recorder 정지가 보호·finalize 아카이브(MP4+metadata)를 그대로 보존함(archiveManager 무접촉)을 증거 보존 불변식으로 명시.
