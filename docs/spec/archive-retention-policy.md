# 아카이브 보존 정책 스펙

> 상태: 살아있는 계약 (living spec) · 독자: 스펙 작성자 / 오케스트레이터
> 단위: recording 서비스 — **영구 아카이브(`archives-data`) 수명주기 계약**
> 관련: `docs/services/recording.md`(서비스 구현 가이드), `docs/spec/archive-download-ux.md`(아카이브 소비 UX), `docs/spec/camera-change-propagation.md`(삭제 증거보존 — 아래 "증거보존과의 관계" 참조), GH #114.
> 접합부: 아카이브 삭제 부작용(보호 세그먼트 해제·메타 정합)은 기존 아카이브 삭제 계약(`DELETE /api/archives/*` → `ArchiveManager.DeleteArchive`)이 소유하는 부작용과 동일 계약을 재사용한다.

## 목적 / 의도

영구 아카이브 볼륨(`archives-data`)에 적재되는 **사고 아카이브(`incident_*`)의 누적 용량이 무한 증가하지 않도록** 보존 상한을 계약으로 고정한다. recording 서비스는 사고(incident)마다 무재인코딩(`-c copy`) MP4를 영구 적재하며 개당 1~3GB에 이르므로, 자동 축출(eviction) 없이는 단일 온프렘 미니PC의 디스크가 고갈된다. 이 단위는 세 가지를 보장한다.

1. **용량 상한(주 정책)**: 축출 대상 사고 아카이브의 총 용량이 `ARCHIVE_MAX_BYTES`를 넘지 않도록, 초과 시 **오래된 것부터(oldest-first)** 삭제한다.
2. **나이 상한(보조 정책, 선택)**: `ARCHIVE_RETENTION_DAYS`를 초과한 사고 아카이브는 용량이 상한 이내여도 축출한다(오래된 사고 영상의 유계 보존 — 규제/프라이버시 보존창이 필요할 때 켠다).
3. **비대상 아카이브 보호**: 사용자가 명시적으로 생성한 아카이브(`manual_*`)와 그 외 비-`incident_` 아카이브는 어떤 자동 축출로도 삭제되지 않는다.

이 계약은 기존 `recordings-data`의 롤링 청소(`ROLLING_WINDOW_MINUTES` 초과 미보호 세그먼트 자동 삭제, `CleanupOldSegments`)와 **같은 성격의 요청-없는 주기 수복**이다. 다만 구조는 다르다: 나이 상한은 롤링 청소·`AutoFinalizeExpired`와 동형(나이 컷오프 sweep)이지만, **용량 상한은 컷오프 필터가 아니라 생성시각 정렬 후 누적-초과분 삭제**다. 삭제는 여전히 안전 해체(보호 세그먼트 해제 + 메타 정합)를 수반한다.

**스코프 경계(정직한 의도)**: 본 계약은 `incident_*` 사고 아카이브의 누적 용량만 상한한다. `manual_*`·`recordings-data`·메타·기타 파일의 볼륨 점유는 본 계약의 직접 대상이 **아니다**. 운영자는 `ARCHIVE_MAX_BYTES`를 (디스크 여유 − 수동 아카이브·롤링 recordings 예상 헤드룸)으로 산정한다. 디스크 전체 고갈 방지의 마지막 안전판은 본 계약 밖이다(별도 도입 시 `GET /api/storage`의 `diskAvailableBytes` 재사용).

### 증거보존(`camera-change-propagation`)과의 관계

`camera-change-propagation`은 "카메라 삭제가 사고 증거 아카이브를 연쇄 purge하지 않는다"를 불변식으로 못박는다. 본 계약의 축출은 **다른 축**이다 — 카메라 삭제 이벤트가 아니라 **용량·나이 초과**를 트리거로, finalize된 사고 증거도 oldest-first로 삭제할 수 있다. 두 계약은 충돌하지 않는다: 무한 적재로 인한 디스크 고갈 방지가 오래된 사고에 대해 우선하며, oldest-first·나이 상한은 **최신 증거를 최대한 보존**한다(용량 하한 케이스에서 최신 완료 아카이브 1건은 항상 보존). 미래에 `camera-change-propagation`의 "cleanup purge 금지" 회귀가드를 구현할 때는, 본 정책의 **의도된 용량·나이 축출**을 그 가드의 예외로 인지해야 한다(카메라 삭제 연쇄가 아니므로).

## 언어 · 런타임

- **서버**: Go (표준 `net/http`), recording 서비스 프로세스.
- **직렬화**: 파일시스템(MP4 디렉터리 + `metadata.json`), REST 관측면은 JSON.

## 의존 도구 · 시스템

- **파일시스템 볼륨 `archives-data`** — 아카이브 디렉터리 집합 + `metadata.json`(목록 SSOT). 각 아카이브 디렉터리 이름은 아카이브 ID이며 `{incidentID}_{streamKey}_{ts}` 형태다.
- **아카이브 분류 (ID 접두어 기반 · `type` 필드 없음)** — `ArchiveMetadata`에는 `type` 필드가 없다. 분류는 **아카이브 ID(=`IncidentID` 접두어)** 로 한다:
  - **사고 아카이브(축출 대상)**: 자동 사고 파이프라인이 부여한 `incident_`로 시작하는 ID(notifier가 `incident_{site}_{ts}`를 `incidentId`로 발급 → recording이 `{incidentId}_{streamKey}_{ts}`로 아카이브 ID 생성).
  - **비대상(보호)**: `manual_`로 시작하는 ID(수동 생성 시 `incidentId` 생략분에 서버가 부여), **및 `incident_`로 시작하지 않는 그 외 모든 ID**(사용자가 임의 `incidentId`를 명시한 수동 산출물 포함). 축출 대상 선정은 **포함 규칙**(`incident_` 접두어)으로 하며, 이는 사용자 제공 산출물을 절대 자동 삭제하지 않는 안전한 기본값이다.
- **아카이브 상태(`Status`)** — enum SSOT: `protecting · pending · finalizing · processing · completed · failed`. `SizeBytes`는 `completed` 전이 시점에만 채워진다(진행 중 아카이브는 `SizeBytes` 미확정, 원본 세그먼트 보호·병합 진행 중).
- **주기 스케줄러** — recording은 이미 30초 티커 고루틴에서 `CleanupOldSegments`·refresh·`AutoFinalizeExpired`를 돌린다. 축출은 **별도 스케줄러를 만들지 않고 이 기존 주기 배선에 편승**한다.
- **아카이브 삭제 부작용 계약** — 아카이브 1건 삭제 시 (a) 디렉터리 제거(`os.RemoveAll`), (b) 그 아카이브가 보호하던 원본 세그먼트의 보호 해제(`unprotectSegments`, 다른 아카이브가 참조하면 유지), (c) 메타 목록(`metadata.json`)에서 참조 제거 + 원자적 저장(`saveMetadata`). 축출은 삭제 대상마다 **기존 `ArchiveManager.DeleteArchive(archiveID)` 경로를 호출**한다(raw 파일 삭제 금지).
- **관측면 REST** — `GET /api/archives`(목록, `metadata.json` 관측면), `GET /api/storage`(사용량). 축출 결과가 이 응답에 반영된다.

## 입력

| 설정 | 의미 | 기본값 | 비활성 조건 |
|------|------|--------|-------------|
| `ARCHIVE_MAX_BYTES` | 축출 대상 사고 아카이브 총 용량 상한(바이트). 초과분은 oldest-first 삭제. | **없음(opt-in)** — 미설정이 기본이며 이때 용량 축출은 발효되지 않는다. `100GiB` 등은 운영자가 명시 설정하는 예시일 뿐 내장 기본값이 아니다. | `0` 또는 미설정 → 용량 축출 비활성 |
| `ARCHIVE_RETENTION_DAYS` | 사고 아카이브 최대 보존 일수. 초과 아카이브는 용량 무관 축출. | **없음(opt-in, 선택)** | `0` 또는 미설정 → 나이 축출 비활성 |

- 대상 파싱은 기존 `ROLLING_WINDOW_MINUTES` env 관례(`strconv` + `>0` 가드)와 대칭. 미설정/비양수는 비활성.
- **대상 집합**: `archives-data`의 아카이브 중 **ID가 `incident_`로 시작하고 `Status == "completed"`인 것만**. 진행 중(`protecting`/`pending`/`processing`/`finalizing`)·`failed`·비-`incident_`(`manual_*` 포함) 아카이브는 용량 산정·나이 산정·삭제 어디에도 포함되지 않는다.
- 아카이브의 "나이"는 **`metadata.json`의 `CreatedAt`(RFC3339, UTC)** 을 유일 기준으로 계산한다. 파일 mtime은 재시작·복사·볼륨 이관으로 변동하므로 나이·정렬에 쓰지 않는다.

## 출력 (계약)

한 번의 축출 주기(cycle)가 끝난 뒤 다음 상태가 보장된다(대상 = "완료된 `incident_*` 아카이브"):

- **용량 불변식**: `ARCHIVE_MAX_BYTES > 0`이면, 대상 아카이브의 총 `SizeBytes` ≤ `ARCHIVE_MAX_BYTES`. **단, 남은 대상이 1건뿐이면 그 1건의 크기까지 허용**한다(oldest-first로 최신 완료 아카이브 1건은 항상 보존 — 단일 아카이브가 상한보다 커도 최신 증거를 지우지 않는다. 이 하한 케이스는 경고 로그로 표면화).
- **나이 불변식**: `ARCHIVE_RETENTION_DAYS > 0`이면, `CreatedAt` 나이가 `ARCHIVE_RETENTION_DAYS`를 초과한 대상 아카이브가 0건.
- **비대상 보존 불변식**: 축출 전 존재하던 모든 비-`incident_` 아카이브(`manual_*` 및 임의-id 수동 산출물)와 진행 중 아카이브가 축출 후에도 그대로 존재한다.
- **순서 불변식(용량 축출 한정)**: **용량 초과**로 삭제가 일어났다면, 삭제된 집합은 `CreatedAt` 오름차순 경계 아래(더 오래된 것)이고 살아남은 대상은 그보다 **더 최근**이다. (나이 축출은 개별 나이 기준이라 이 순서 불변식의 대상이 아니다.)
- **정합 불변식**: 축출은 `DeleteArchive` 경로를 경유하므로 삭제된 아카이브는 목록 SSOT(`metadata.json` → `GET /api/archives`)에 더 이상 나타나지 않고, 실제 디렉터리도 제거되며(양쪽 부재), 보호 세그먼트가 영구 잔존하지 않는다(댕글링 없음). 댕글링 부재·SSOT 정합은 재사용된 삭제 계약이 보증한다.
- **무해 불변식**: 상한 이내이고 나이 초과 아카이브가 없으면(또는 두 임계값 모두 비활성이면), 축출 주기는 **아무것도 삭제하지 않는다**(no-op).

## 핵심 로직 (동작)

- **주기 실행**: 축출은 외부 요청 없이 기존 30초 티커 고루틴에서 반복된다(`CleanupOldSegments`와 동일 주기 배선에 편승, 신규 스케줄러 없음). 초과 상태는 다음 주기 안에 자기수복(self-heal)된다.
- **결정적 진입점**: 축출 로직은 티커와 분리된 **직접 호출 가능 함수**로 노출한다 — 순수 선정 함수 `selectEvictions(archives []ArchiveMetadata, maxBytes int64, retentionDays int, now time.Time) []string`(삭제 대상 ID 반환, 부작용 0)과 그것을 `DeleteArchive`에 결선하는 얇은 래퍼(예 `EvictArchives(now)`). `now`는 주입 가능(나이/자기수복 판정 결정성). 이는 `CleanupOldSegments(window)`가 인자를 받아 티커 없이 1회 실행 가능한 것과 대칭이다.
- **용량 우선(주 정책)**: 대상(완료 `incident_*`) 총 `SizeBytes`가 `ARCHIVE_MAX_BYTES`를 초과하면, `CreatedAt` 오름차순으로 가장 오래된 것부터 삭제해 총 용량을 상한 이하로 되돌린다(최신 1건은 보존 하한).
- **나이(보조 정책)**: `ARCHIVE_RETENTION_DAYS`를 초과한 대상은 용량이 상한 이내여도 삭제한다. 두 정책이 함께 켜져 있으면 합집합으로 축출한다(나이 초과분 + 용량 초과분).
- **정렬 안정성**: oldest-first 정렬 키는 `(CreatedAt asc, ID asc)` — `CreatedAt`가 동일 초(RFC3339 초 해상도)로 동률이면 ID 사전순으로 결정적 tie-break.
- **비대상 제외**: 비-`incident_`(`manual_*`·임의-id 수동)·진행 중·`failed`은 용량 산정·나이 산정·삭제 대상 어디에도 포함되지 않는다. 대상만으로 볼륨이 상한을 넘어도, 비대상 아카이브는 자동 삭제하지 않는다(사용자 명시 산출물·활성 사고 증거 보호).
- **안전 해체**: 삭제는 삭제 대상마다 `DeleteArchive(archiveID)`를 호출한다(보호 세그먼트 해제 + 메타 참조 제거 + 원자 저장을 그 함수가 수행). 부분 실패 시에도 메타와 실제 디렉터리가 어긋난 댕글링 상태를 남기지 않는다.
- **비활성 반영**: 두 임계값은 각각 독립적으로 끌 수 있다(`0`/미설정). 둘 다 비활성이면 축출은 일어나지 않는다(opt-in 미설정 시 기존 무한 적재 동작과 동일 — 명시적 비활성 선택일 때만).

## 검증 단언 (TDD)

> 판정 전략(정정): 삭제 대상 선정은 **순수 함수 `selectEvictions(...)`** 로 분리되어 부작용 없이 결정적이다. 따라서 **A·B·C·D·E·G는 이 순수 함수의 in-process 테이블 단위 테스트로 판정**한다(Docker·HTTP·mutating 부작용 없음 — SKIP 아님). **F(정합)와 삭제 배선은 `t.TempDir()`에 메타·디렉터리를 시드하고 `DeleteArchive` 왕복으로 in-process 판정**한다(레포의 `cleanup_test.go`·`archive_evidence_preservation_test.go` 선례와 동형). **H(주기 고루틴 자기수복)와 A/B/F의 end-to-end REST 관측만 격리 스택으로 판정**한다. 시딩은 크기(`SizeBytes`)·`CreatedAt`·ID 접두어(`incident_`/`manual_`)·`Status`를 제어한 더미 메타로 구성한다.

- **A (핵심)** — 용량 상한: `ARCHIVE_MAX_BYTES = X`, 완료 `incident_*` 총 `SizeBytes`가 `X`를 초과하도록 시딩 → `selectEvictions` 1회(또는 격리 스택 1주기) → 이후 대상 총 용량 ≤ `X`(남은 대상 1건 하한 예외 포함). (in-process: `SizeBytes` 합으로 관측. 격리 스택: `GET /api/storage`는 manual 포함·15초 캐시라 대상-합의 직접 관측면이 아님에 주의 — 대상 아카이브 실측 또는 캐시 무효화 후 관측.)
- **B (핵심)** — 비대상 보존: `manual_*` 1건 + 임의-id 수동 1건 + 상한 초과 완료 `incident_*` 다수를 시딩 → 1주기 → 두 비대상이 여전히 존재하고 목록에 남아 있다.
- **C (핵심)** — oldest-first 순서: 서로 다른 `CreatedAt`의 완료 `incident_*` N건을 용량 초과로 시딩 → 1주기 → 살아남은 집합은 **가장 최근** 아카이브들이고, 가장 오래된 것들이 삭제되었다(`(CreatedAt, ID)` 오름차순으로 삭제 경계 형성; 동일 초 tie는 ID 사전순으로 결정적).
- **D** — 나이 상한: `ARCHIVE_RETENTION_DAYS = R`, 용량은 상한 이내로 두고 `CreatedAt` 나이가 `R`을 초과한 완료 `incident_*` 1건 + `R` 이내 1건을 시딩(주입 `now`) → 1주기 → 초과분 삭제, 이내분 보존.
- **E (핵심)** — 무해(no-op): 용량이 상한 이내이고 나이 초과 아카이브가 없는 상태 → 1주기 → 목록과 총 용량이 **주기 전후 동일**(아무것도 삭제되지 않음).
- **F (핵심)** — 정합: A/C에서 삭제가 발생한 뒤, 삭제가 `DeleteArchive` 경로를 경유하여 삭제된 id가 목록 SSOT(`metadata.json`/`GET /api/archives`)에 없고, 실제 디렉터리도 제거되었으며(양쪽 부재·댕글링 없음), 완료 아카이브에 한해 메타 엔트리와 디렉터리가 상호 대응한다(`metadata.json` 파일·`.tmp` 등 비-아카이브 항목은 비교에서 제외).
- **G** — 비활성 의미: `ARCHIVE_MAX_BYTES = 0`(미설정)이면 용량이 임의로 커도 용량 축출이 일어나지 않는다. `ARCHIVE_RETENTION_DAYS = 0`(미설정)이면 임의로 오래된 아카이브도 나이 축출되지 않는다(명시적 비활성일 때만 무한 적재).
- **H** — 주기성/자기수복: 상한을 초과한 상태로 두고 요청을 보내지 않아도, 티커 1주기 경과 후(격리 스택) 용량 불변식이 회복된다(외부 API 호출 없이 주기 실행만으로 수복).

## 검증 스킵 선언 (정정 — 대부분 판정 가능)

- **A·B·C·D·E·F·G — SKIP 아님**: 삭제 대상 선정이 순수 함수(`selectEvictions`)로 분리되고 부작용(F·삭제)은 `t.TempDir()` + `DeleteArchive` in-process 왕복으로 판정되므로, 이 7개는 mutating 격리 스택 없이 **in-process 단위 테스트로 non-vacuous하게 판정**한다(레포 선례 `cleanup_test.go`·`archive_evidence_preservation_test.go`). `golang:1.22-alpine` 컨테이너에서 `go test` 실행(호스트 무설치).
- **H — 격리 스택 판정**: 주기 고루틴 타이밍(요청 없는 자기수복)은 실제 티커·프로세스가 필요하므로 격리 검증 스택(`verify/archive-retention/`: 격리 docker-compose + 제어된 시드 + prod 무접촉 포트)에서 판정한다. 이때 A·B·F도 `GET /api/archives`/`GET /api/storage` end-to-end 관측으로 이중 확인한다.
- 격리 스택으로도 판정 못 하고 **남는 스킵만** RESULT.md에 표면화한다(핵심(load-bearing) 스킵 = 사람 승인 대상). 중요도: A·B·C·E·F는 **핵심(load-bearing)**(용량 상한·비대상 보존·순서·무해·정합이 이 단위의 3대 보장을 구성), D·G·H는 일반. — 위 전략상 핵심 단언은 in-process로 판정되어 SKIP으로 남지 않는 것이 목표다.
