# recording 스펙

> 상태: 살아있는 계약 (living spec) · 독자: 스펙 작성자

## 목적 / 의도

- 모든 활성 카메라의 영상을 **끊김 없이 상시 녹화**하여, 위기(incident) 발생 시 "사건 전 1시간 ~ 상황 종료 후 30분" 구간의 증거 영상을 반드시 확보할 수 있게 한다.
- 저장 공간은 유한하므로, 보호되지 않은 녹화분은 **롤링 윈도우**(기본 60분)만 유지하고 자동 삭제한다.
- incident 영상은 두 단계 계약(**protect → finalize**)으로 보존한다: 위기 감지 즉시 삭제만 막고(protect), 상황 종료 후 하나의 MP4로 병합해 영구 아카이브를 만든다(finalize).
- 과거 구간을 브라우저에서 즉시 돌려볼 수 있도록, 저장된 세그먼트로부터 **VOD형 HLS playlist를 동적 생성**한다.
- 전 구간 **무 트랜스코딩** — 녹화·병합 모두 재인코딩하지 않고 원본 코덱을 유지한다 (mini PC CPU 보호).

## 언어 · 런타임

- Go 1.22, 표준 라이브러리만 사용 (외부 Go 의존성 0).
- 단일 정적 바이너리로 빌드되어 Docker 컨테이너(`sentinel-ffmpeg-base` 기반)에서 실행된다.
- 내부 HTTP 포트 8080.

## 의존 도구 · 시스템

- **FFmpeg** (컨테이너 내 필수): RTMP 수신·세그먼트 분할, 세그먼트 병합 모두 FFmpeg 하위 프로세스로 수행한다.
- **streaming 서비스** (RTMP pull 원천): 이 접면의 계약 소유자는 [docs/spec/interface-streaming.md](interface-streaming.md) §계약 3 (RTMP 라이브 재배포) — push 없는 streamKey의 pull은 데이터 없이 대기/실패하며 재시도 책임은 본 서비스에 있다. RTMP 스트림 규격(코덱 등 — 허브는 B-frame 포함 H.264도 수용)은 같은 문서 §계약 1이 소유하며 본 스펙은 재정의하지 않는다.
- **web-backend**: 카메라 목록 원천(`GET /internal/cameras` — 계약 소유자 [docs/spec/interface-web-api.md](interface-web-api.md) §계약 13) 및 본 서비스 `/api/*`의 인증 프록시(외부 노출 계약 소유자 [docs/spec/interface-web-api.md](interface-web-api.md) §계약 8).
- **볼륨 2개**: 롤링 세그먼트 저장소(기본 `/recordings`), 영구 아카이브 저장소(기본 `/archives`). 컨테이너 재시작에도 파일은 유지된다.
- DB 없음. 아카이브 메타데이터는 아카이브 볼륨 내 단일 JSON 파일로 영속화된다.

## 입력

- **RTMP 스트림**: `{STREAMING_RTMP_URL}/{streamKey}` (기본 `rtmp://streaming:1935/live/{streamKey}`). 카메라당 1개의 지속 연결.
- **카메라 목록**: web-backend `GET /internal/cameras` 응답 중 `enabled == true`이고 `streamKey`가 비어있지 않은 항목만 녹화 대상이다. 기동 시 1회(최대 10회, 3초 간격 재시도) + `POST /api/cameras/reload` 시마다 조회.
- **HTTP 요청** (web-backend가 인증 후 프록시; 직접 호출 시 무인증·내부망 전제):
  - 시간 파라미터(`from`, `to`)는 RFC3339(ISO8601)를 받는다.
  - `incidentTime`, `resolvedAt`은 RFC3339 우선, 실패 시 `"2006-01-02 15:04:05"` 형식도 허용한다.
- **환경 변수**: `STREAMING_RTMP_URL`, `WEB_BACKEND_URL`, `RECORDINGS_DIR`, `ARCHIVES_DIR`, `ROLLING_WINDOW_MINUTES`(기본 60, 양의 정수만 유효), `FFMPEG_TIMEOUT`(초, 기본 60, 양의 정수만 유효). 미설정/무효 값이면 기본값으로 동작한다.

## 출력 (계약)

### 파일 시스템

- **롤링 세그먼트**: `{RECORDINGS_DIR}/{streamKey}/{YYYYMMDD_HHMMSS}.ts`
  - 파일명은 **UTC** 타임스탬프. 세그먼트는 10초 길이로 분할되며 경계가 벽시계 시각(10초 배수)에 정렬된다. MPEG-TS 컨테이너, 코덱 원본 유지.
  - 보호되지 않은 세그먼트는 파일명 타임스탬프(파싱 불가 시 mtime) 기준으로 롤링 윈도우 초과 시 삭제된다.
  - 0바이트 세그먼트는 보호 여부와 무관하게 삭제된다.
- **영구 아카이브**: `{ARCHIVES_DIR}/{archiveId}/{streamKey}.mp4`
  - `archiveId = {incidentId}_{streamKey}_{fromUTC(YYYYMMDD_HHMMSS)}` — 같은 (incident, streamKey, from)에 대한 중복 생성 요청은 기존 항목을 반환하며 새로 만들지 않는다.
  - MP4는 다운로드 완료 전에도 재생을 시작할 수 있는 형태(메타데이터 선행 배치)로 생성되며, 세그먼트들의 무손실 병합본이다.
- **아카이브 메타데이터**: `{ARCHIVES_DIR}/metadata.json` — 전체 아카이브 목록의 SSOT. 상태 변화마다 저장되고 재시작 시 로드된다.
  - 각 아카이브 항목은 병합 구간 경계 `from`/`to`(RFC3339, UTC)를 보유하며, 이 값은 **모든 비종단 상태(`protecting`·`pending`·`processing`·`finalizing`)에서 영속·보존된다** — 재시작 후 기동 복구가 원본 구간을 알 수 있는 근거다(§핵심 로직 7). 종단 전이(`completed`/`failed`) 시에도 유지된다.
  - 각 아카이브 항목은 완료 타임스탬프 `completedAt`(RFC3339, **UTC 고정**)을 가진다: `status == "completed"`일 때 **non-null**, 그 외(미완료 4종·`failed`)에는 **null/부재**다. `completedAt`은 `completed` 전이 시 `status`·`sizeBytes`·`filePath`와 **원자적으로**(한 잠금 안에서) 기록되어, 소비자가 이 넷 중 하나만 참인 중간 상태를 관측하지 않는다. 값의 정본은 UTC이며 로컬 표시 변환은 소비자(web-frontend) 몫이다.
  - 실패 사유 필드명은 `lastError`(사람이 읽을 수 있는 문자열)로 통일한다 — `status == "failed"`인 항목은 **비어있지 않은 `lastError`**를 가진다(아래 §핵심 로직 4·복구 실패 경로 + 단언 P-2 참조). 이 필드의 JSON 키는 `lastError`다.

### HTTP API (응답 계약 요지)

| Method·Path | 보장 |
|---|---|
| `GET /healthz` | 200, `{"status":"ok","service":"recording"}` |
| `GET /api/status` | 활성 카메라별 `{streamKey, status, startedAt, lastError, segmentDir}` 배열. `status ∈ {recording, reconnecting, disconnected}` |
| `POST /api/cameras/reload` | 최신 카메라 목록으로 recorder를 reconcile(추가된 카메라 시작, 제거된 카메라 중지). 항상 200 `{"status":"reloaded","cameras":N}`. web-backend 조회 실패(연결 오류·본문 디코드 실패; HTTP 상태코드는 검사하지 않음) 시에도 오류를 반환하지 않고 **빈 목록으로 reconcile하여 모든 recorder를 중지**하며 200 `{"status":"reloaded","cameras":0}`을 반환한다 (⚠️ 8) |
| `GET /api/recordings/{key}` | 사용 가능 구간 `{streamKey, timeRanges:[{start,end}]}` (RFC3339). 세그먼트 간격 15초 이하면 하나의 연속 구간으로 병합. 녹화 없으면 404 |
| `GET /api/recordings/{key}/play?from=&to=` | `application/vnd.apple.mpegurl`, VOD형 playlist(`EXT-X-PLAYLIST-TYPE:VOD` + `EXT-X-ENDLIST`), 구간과 겹치는 세그먼트를 시간순 나열. 해당 세그먼트 없으면 404. `from`/`to` 누락·형식 오류는 400 |
| `GET /api/recordings/{key}/segments/{file}` | `.ts` 파일을 `video/mp2t`로 서빙. 경로 탈출(`/`, `\`, `..`) 및 `.ts` 외 확장자는 400 |
| `POST /api/archives/protect` | (Phase 1) 202. incident별·streamKey별 상태 `protecting` 아카이브 항목 생성. `incidentTime − 1시간`부터의 세그먼트를 삭제 보호. 이후 도착하는 세그먼트도 주기적으로 계속 보호 |
| `POST /api/archives/finalize` | (Phase 2) 해당 incident의 `protecting` 아카이브들을 `resolvedAt + 30분`을 종료 시각으로 백그라운드 병합 시작. `protecting`이 하나도 없으면 404. **종료 시각(`To`)은 메타 상한**이며 병합이 즉시 실행되므로 `resolvedAt`이 현재 시각에 가까우면 그 이후 post-roll 세그먼트는 디스크에 없어 MP4에 포함되지 않는다(과대표기 가능, ⚠️ 리뷰 필요 2) |
| `POST /api/archives` | 임의 구간 즉시 아카이브(수동). 202 + 생성된 항목들. `incidentId` 생략 시 `manual_*` 자동 부여 |
| `GET /api/archives` | 전체 아카이브 메타 배열. `status`는 **아카이브 status enum의 정본(정의 SSOT=본 스펙)**이며 정확히 6종 `{protecting, pending, finalizing, processing, completed, failed}` 중 하나다. `completed` 항목은 non-null `completedAt`(RFC3339 UTC)을, `failed` 항목은 non-empty `lastError`를 함께 노출한다. 이 enum을 소비하는 web-frontend의 의무(6종 전부 처리·미지 상태 안전 fallback·`failed` 노출·`completedAt` 로컬 표시)는 [interface-web-api.md](interface-web-api.md) §계약 8이 판정 가능한 소비자 계약으로 규정한다 |
| `GET /api/archives/{id}/download` | `completed`만 `video/mp4` + attachment로 서빙. 그 외 **모든 비-`completed`**(미완료 4종 `protecting`/`pending`/`finalizing`/`processing` **및 종단 `failed`**)는 비-2xx **409**(부분·0바이트 미디어를 2xx로 내려보내지 않음), 아카이브 부재는 **404** |
| `DELETE /api/archives/{id}` | 아카이브 디렉터리·메타 삭제. 없으면 404 |
| `DELETE /api/archives/incident/{incidentId}` | 해당 incident의 모든 아카이브 삭제 + 삭제 수 반환. 없으면 404 |
| `GET /api/storage` | `{recordingsBytes, archivesBytes, totalUsedBytes, archiveCount, diskTotalBytes, diskUsedBytes, diskAvailableBytes}` |

- playlist가 가리키는 세그먼트 URL(`/api/recordings/...`)의 외부 노출 형태·인증은 web-backend 프록시 계약([docs/spec/interface-web-api.md](interface-web-api.md) §계약 8 소유)을 따른다.

## 핵심 로직 (동작)

1. **상시 녹화 루프 (카메라당 1개)**
   - FFmpeg로 RTMP를 pull하여 무 트랜스코딩으로 10초 `.ts` 세그먼트를 쓴다.
   - 연결 실패/종료 시 exponential backoff **1초 → 최대 30초**로 무한 재시도한다. 성공적으로 시작되면 backoff는 1초로 리셋된다.
   - 상태 전이: 시작 시도 중 `reconnecting` → 프로세스 기동 성공 `recording` → 종료/실패 `disconnected`(+ `lastError` 기록).
2. **Watchdog**: liveness 신호는 상시 녹화 FFmpeg의 **진행(progress) 스트림**이다 — 상시 녹화기는 전용 fd(fd 3)로 구조화된 진행 정보(frame=/out_time=/progress=continue 등)를 약 0.5초 간격으로 내보내며(`-progress pipe:3`, Go 측 `cmd.ExtraFiles`로 파이프 배선), watchdog는 이 진행 업데이트의 **마지막 수신 시각**을 기준으로 한다. `FFMPEG_TIMEOUT`초 동안 진행 업데이트가 없으면 stall로 간주, SIGTERM → 5초 후 SIGKILL로 종료시키고 재연결 루프에 맡긴다. 로그(stdout/stderr)는 liveness 판정에 쓰지 않는다 — `-loglevel warning` + `-c copy` 정상 녹화는 로그가 거의 없어 무출력이 곧 hang이 아니기 때문이다(#68). 프로세스가 얼어붙으면(예: SIGSTOP) 진행 방출도 멈추므로 실제 hang은 여전히 감지된다. (주의: 이 watchdog는 **상시 녹화기에만** 적용된다. finalize/아카이브 병합용 일회성 FFmpeg에는 적용되지 않는다.)
3. **롤링 정리 (30초 주기)**: 모든 스트림 디렉터리에서 (a) 0바이트 `.ts`는 무조건 삭제, (b) 미보호이면서 롤링 윈도우보다 오래된 `.ts`를 삭제한다. 보호 목록은 in-memory 파일 경로 집합이다.
4. **incident 2단계 아카이브**
   - **protect**: `incidentTime − 1시간 ~ 현재`의 기존 세그먼트를 보호 집합에 넣고, streamKey별 `protecting` 메타를 만든다.
   - **보호 갱신 (30초 주기)**: `protecting` 상태인 동안 새로 생기는 세그먼트도 계속 보호 집합에 추가된다 — 즉 protect 이후 도착 영상도 삭제되지 않는다.
   - **finalize**: 종료 시각을 `resolvedAt + 30분`으로 확정하고, 구간 내 세그먼트를 보호 처리 후 재인코딩 없이 단일 MP4로 백그라운드 병합한다. 성공 시 `completed`(+파일 크기 `sizeBytes` + `filePath` + `completedAt` **원자적 기록**), 실패 시 `failed`(+ **non-empty `lastError` 사유**)로 기록된다. 종단 상태(`completed`/`failed`)는 이후 뒤집히지 않는다(단조성). **모든 `failed` 종단 전이**(이 finalize-직접실패 경로 포함, 아래 복구 실패 경로 및 단언 P-2와 동일)는 non-empty `lastError`를 남긴다. 단, 병합은 즉시 실행되므로 `resolvedAt`이 현재 시각에 가까우면 `To`(=resolvedAt+30분) 이후의 post-roll 세그먼트는 아직 디스크에 없어 MP4에 포함되지 않는다 — 메타의 `To`는 실제 병합 범위의 **상한(과대표기 가능)**이다(⚠️ 리뷰 필요 2).
   - **자동 finalize (30초 주기)**: `protecting` 상태가 incident 발생 후 **2시간**을 넘기면 finalize 누락으로 간주하고 현재 시각 기준으로 자동 finalize한다 — 보호가 무한히 지속되어 디스크가 차는 것을 막는 안전장치.
5. **pseudo-playback**: 요청 구간 `[from, to)`와 **겹치는**(경계 포함 판정: 세그먼트 종료 > from AND 세그먼트 시작 < to) 세그먼트를 시간순으로 나열한 VOD playlist를 매 요청마다 동적 생성한다. 세그먼트 길이는 10초로 간주한다.
6. **reconcile**: `POST /api/cameras/reload`는 web-backend 카메라 CRUD 전파(생성·수정·삭제)의 **정식 소비자**다 — web-backend가 DB 쓰기 성공 후 세 소비자(cctv-adapter·youtube-adapter·recording)에 비동기·최선노력으로 팬아웃하고, recording은 수신 즉시 `GET /internal/cameras`를 재조회해 재조정한다(SSOT: `docs/spec/camera-change-propagation.md` 계약 1). reload 시 streamKey 기준으로만 비교한다 — 새 key는 녹화 시작, 사라진 key는 SIGTERM(3초 후 SIGKILL)으로 중지. 동일 key의 다른 속성 변경은 녹화에 영향 없다. **증거 보존 불변식**: 사라진(카메라 삭제된) key의 recorder 정지는 stopCh·SIGTERM·states 삭제만 수행하며 `archiveManager`·`metadata.json`·아카이브 파일에 **접근하지 않는다** — 그 streamKey로 키잉된 보호·finalize(`completed`) 아카이브(병합 MP4 + 메타)는 reconcile 이후에도 그대로 보존된다(purge 없음; `camera-change-propagation.md` 계약 2). 카메라 목록 조회 실패는 "카메라 0대"와 동일하게 취급되어 실행 중인 모든 recorder가 중지된다 (⚠️ 8).
7. **재시작 내성**: 메타데이터 JSON은 재시작 후 로드된다. `protecting` 아카이브는 보호 갱신 주기에 의해 재시작 후에도 세그먼트 보호가 복원된다.
   - **미완료 아카이브 복구 (기동 시)**: 병합은 in-process 작업이므로 재시작하면 스스로 이어지지 않는다. 따라서 로드 직후, 비종단(non-terminal) 상태이면서 `protecting`이 아닌 아카이브(`pending`·`processing`·`finalizing`)에 대해 기동 복구를 수행한다. 종단 상태는 `completed`·`failed` 두 가지뿐이며, 재시작을 넘겨 비종단 상태에 무기한 고착되는 아카이브는 없어야 한다.
   - **보호 우선 재확립 (순서 계약)**: 기동 시퀀스는 **① metadata 로드 → ② 모든 복구 대상 아카이브(비종단·비-`protecting`)의 병합 구간 `[from, to)` 세그먼트를 보호 집합에 등록 → ③ 롤링 정리 루프의 최초 실행** 순서를 지킨다. 즉 복구 대상 보호 등록은 첫 cleanup 실행보다 **먼저 완료**된다(단순 순서 불변식 — 락·배리어 등 정교한 동기화는 요구하지 않는다). 결과적으로 진행 중이던 병합에 필요한 원본 `.ts`가 재시작의 부작용으로 롤링 cleanup에 삭제되는 일은 없다. 이 순서는 로그 마커로 관측 가능하다: 보호 재확립 완료 시 `Recovery protection re-established`를, cleanup 최초 실행 시 `Rolling cleanup started`를 남기며, 전자가 후자보다 먼저 나타난다.
   - **복구 방향 = 재개, 실패는 표식**: 재확립된 보호 위에서 병합을 다시 실행(재개)한다. 성공하면 `completed`(+크기), 필요한 세그먼트가 이미 소실되었거나 병합이 오류로 끝나면 `failed`(+사유)로 종단 전이시킨다. 어느 경우든 최종적으로 종단 상태에 도달한다.

## 검증 단언 (TDD)

각 단언은 컨테이너 기동 상태에서 OK/NOK 판정 가능해야 한다. (`$REC` = recording 컨테이너 내부 또는 내부망에서 `http://recording:8080`)

- **A. 헬스**: `curl -fsS $REC/healthz` 가 200이고 body가 `{"status":"ok","service":"recording"}` 이다.
- **B. 세그먼트 생성**: 활성 RTMP 스트림 `k`가 존재할 때, 30초 대기 후 `{RECORDINGS_DIR}/k/` 안에 정규식 `^\d{8}_\d{6}\.ts$` 파일이 2개 이상 새로 생기고, 최신 파일의 파일명 타임스탬프(UTC)와 현재 UTC의 차가 30초 이내다.
- **C. 상태 보고**: 단언 B 상황에서 `GET /api/status` 응답 중 `k` 항목의 `status == "recording"` 이고 `startedAt`이 RFC3339다. 스트림 발행을 중단하면 `1.5 × FFMPEG_TIMEOUT + 15초` 이내에 `status`가 `reconnecting` 또는 `disconnected`로 바뀐다 (로그에 `FFmpeg progress stalled` 또는 `FFmpeg exited` 존재). (시한 근거: FFmpeg가 unpublish에 즉시 종료하면 수 초 내 전이되지만, 즉시 종료하지 않는 hang 경로는 watchdog의 `FFMPEG_TIMEOUT/2` 주기 검사(감지 최대 1.5×timeout 지연) + SIGTERM 후 5초 유예를 거친다 — 기본 60초 설정에서 최악 약 95초)
- **D. 롤링 삭제**: `ROLLING_WINDOW_MINUTES=1`로 기동한 뒤, 파일명이 5분 전인 미보호 더미 `.ts`(1바이트 이상)를 만들어 두면 90초 이내에 삭제된다. 반대로 롤링 윈도우 이내 파일명의 세그먼트는 남아 있다.
- **E. 0바이트 정리**: 0바이트 `.ts`를 스트림 디렉터리에 만들어 두면 (보호 등록 여부와 무관하게) 60초 이내에 삭제된다.
- **F. protect가 삭제를 막는다**: `ROLLING_WINDOW_MINUTES=1` 환경에서 `POST /api/archives/protect` (`incidentId`, `streamKeys:[k]`, `incidentTime=현재`)가 202를 반환하고, 그 시점에 존재하던 `k`의 세그먼트들이 3분(윈도우의 3배) 후에도 삭제되지 않고 남아 있다. 또한 protect **이후** 새로 생성된 세그먼트도 같은 기간 남아 있다.
- **G. finalize가 MP4를 만든다**: 단언 F 이후 `POST /api/archives/finalize` (`incidentId`, `resolvedAt=현재`)가 200을 반환하고, 60초 이내에 `GET /api/archives`에서 해당 항목이 `status=="completed"`, `sizeBytes > 0`이 되며, `{ARCHIVES_DIR}/{archiveId}/k.mp4`가 존재하고 `ffprobe`로 유효한 MP4로 판독된다. `GET /api/archives/{id}/download`는 200 + `Content-Type: video/mp4`다.
- **H. finalize 게이트**: `protecting` 항목이 없는 incidentId에 대한 finalize는 404다. `completed` 이전 아카이브의 download는 409다.
- **I. playback 계약**: 세그먼트가 존재하는 구간에 대해 `GET /api/recordings/k/play?from=&to=`가 200 + `application/vnd.apple.mpegurl`이고, body에 `#EXT-X-PLAYLIST-TYPE:VOD`와 `#EXT-X-ENDLIST`가 있으며, 나열된 각 세그먼트 URL을 GET하면 200 + `video/mp2t`다. 세그먼트가 전혀 없는 구간이면 404, `from` 누락이면 400이다.
- **J. 경로 탈출 차단**: `GET /api/recordings/k/segments/..%2Fmetadata.json` 류의 요청(디코드 후 `..`/`/` 포함 또는 비-`.ts`)은 400이며 파일 내용이 유출되지 않는다.
- **K. 시간 범위 병합**: `k`에 연속 세그먼트만 있을 때 `GET /api/recordings/k`의 `timeRanges` 길이가 1이고, 20초 이상의 공백을 만들면(중간 파일 삭제) 길이가 2가 된다.
- **L. reload reconcile**: web-backend 카메라 목록에서 `k`를 비활성화한 뒤 `POST /api/cameras/reload` 하면, 이후 `GET /api/status`에 `k`가 나타나지 않고 새 세그먼트 생성이 멈춘다. 다시 활성화 + reload 하면 녹화가 재개된다.
- **M. 자동 finalize**: `incidentTime`을 3시간 전으로 지정해 protect만 해두면, 60초 이내에 해당 아카이브가 `protecting`을 벗어나 `finalizing`/`processing`/`completed`/`failed` 중 하나로 전이된다 (로그에 `Auto-finalizing expired incident` 존재).
- **N. 저장 통계**: `GET /api/storage` 응답에 `recordingsBytes`, `archivesBytes`, `totalUsedBytes`, `archiveCount`, `diskTotalBytes`, `diskAvailableBytes`가 모두 존재하고 0 이상의 수치다.
- **O. 재시작 내성**: 아카이브가 1개 이상 있는 상태에서 컨테이너 재시작 후 `GET /api/archives` 결과가 재시작 전과 동일한 ID 집합을 반환한다 (`metadata.json` 로드). `protecting` 항목이 있었다면 재시작 60초 후에도 해당 구간 세그먼트가 롤링 삭제되지 않는다.
- **P. 미완료 아카이브 복구 — 세그먼트 존재 → `completed` 수렴**: `ROLLING_WINDOW_MINUTES=1` 환경에서, **병합 구간에 유효 세그먼트가 존재하는** 아카이브의 `metadata.json` 상태를 `processing`·`finalizing`·`pending` 각각으로 조작해 둔 채 컨테이너를 재시작한다 → 각 setup에 대해 (a) 재시작 3분(윈도우의 3배) 후에도 그 구간의 원본 `.ts`가 롤링 삭제되지 않고 남아 있으며(보호 재확립), (b) 로그에서 `Recovery protection re-established`가 `Rolling cleanup started`보다 **먼저** 출현하고(보호 우선 순서 계약, §핵심 로직 7), (c) 재시작 60초 이내에 해당 아카이브가 비종단 상태를 벗어나 **`completed`**(재개 성공, `sizeBytes > 0` + MP4 생성)로 전이한다. 세 setup(`processing`/`finalizing`/`pending`) 모두에서 성립하면 OK. (세그먼트가 온전한데 `failed`로 끝나면 NOK — 유효 입력에 대한 판정은 `completed`로만 수렴해야 한다.)
- **P-2. 미완료 아카이브 복구 — 세그먼트 소실 → `failed` 종단 강제**: `ROLLING_WINDOW_MINUTES=1` 환경에서, 아카이브의 `metadata.json` 상태를 비종단(`processing`·`finalizing`·`pending` 중 하나)으로 조작하되 **병합 구간 `[from, to)`에 필요한 원본 `.ts`를 모두 제거**(사전 롤링 삭제/아카이브 소실 재현)한 채 컨테이너를 재시작한다 → 재시작 60초 이내에 해당 아카이브가 **`failed`**로 종단 전이하고 `lastError`(사유)가 비어있지 않다. 세그먼트가 없는데 `completed`가 되거나(불가능한 성공 표기), 60초를 넘겨 비종단 상태에 머무르면 NOK. 어떤 아카이브도 재시작을 넘겨 `pending`/`processing`/`finalizing`에 무기한 고착되지 않아야 하며, 복구 불가 케이스는 반드시 `failed`+사유로 종단해야 한다.

## ⚠️ 리뷰 필요 (의도 불확실)

1. **아카이브 삭제 시 세그먼트 보호가 해제되지 않음.** 삭제 흐름이 "디렉터리 삭제 → 보호 해제 판단 → 목록에서 제거" 순서인데, 보호 해제 판단 시점에 삭제 대상 아카이브 자신이 아직 목록에 남아 있고 제외 ID를 지정하지 않아, "다른 아카이브가 참조 중"으로 항상 판정된다. 결과적으로 DELETE 후에도 원본 `.ts`가 계속 보호되어 재시작 전까지 롤링 삭제에서 제외된다(디스크 점유 지속). 의도된 보수적 동작인지, 해제 로직의 결함인지 확인 필요.
2. **finalize의 "+30분 post-roll"이 실제 MP4에 담기지 않을 수 있음.** finalize는 종료 시각을 `resolvedAt + 30분`으로 기록하지만 병합은 즉시 실행되므로, resolvedAt이 현재 시각이면 그 이후 도착할 30분치 세그먼트는 디스크에 없어서 병합에 포함되지 않는다. 메타의 `To`와 실제 영상 범위가 달라진다. post-roll이 의도라면 병합을 지연하거나 재병합이 필요하고, 아니면 `To` 기록이 과대표기다.
3. **0바이트 세그먼트를 보호 여부와 무관하게 삭제.** FFmpeg가 세그먼트를 막 연 직후(순간적으로 0바이트)인 현재 진행 중 파일이 cleanup 주기와 겹치면 기록 중 파일이 삭제될 수 있는 경쟁 창이 있다. "빈 파일은 증거 가치가 없다"는 의도로 보이나(US-003), 진행 중 파일 제외(예: mtime 최근 N초 제외) 없이 무조건 삭제하는 것이 의도인지 확인 필요.
4. ~~재시작 시 진행 중(`pending`/`processing`/`finalizing`) 아카이브가 영구히 그 상태로 남음.~~ **[해소 — 계약화됨]** §핵심 로직 7 "미완료 아카이브 복구"로 승격. 기동 시 비종단·비-`protecting` 아카이브는 보호를 우선 재확립한 뒤 병합을 재개하고, 재개 불가 시 `failed`로 종단 전이한다(단언 P). 재시작을 넘겨 비종단 상태에 고착되는 아카이브는 없다.
5. **타임존 없는 시간 형식(`"2006-01-02 15:04:05"`)을 UTC로 해석.** protect/finalize의 fallback 파싱이 UTC 가정이라, 호출자가 로컬(KST) 시각을 보내면 보호 구간이 9시간 어긋난다. 이 fallback을 쓰는 호출자가 UTC를 보내는 것이 계약인지 확인 필요.
6. ~~**watchdog이 "출력 없음 = 고장"을 전제.**~~ **[해결됨 · #68, B안 확정]** liveness 신호를 로그 출력(stdout/stderr) 유무에서 상시 녹화기의 **진행(progress) 스트림(fd 3, `-progress pipe:3`)** 으로 전환하여 본문(핵심 로직 #2, 단언 C)에 승격했다. `-loglevel warning` + `-c copy` 정상 녹화는 로그가 침묵해도 진행 업데이트를 계속 방출하므로 더 이상 hang으로 오판해 재시작하지 않으며(상시 녹화 gap·증거영상 신뢰성 저하 제거), 실제 동결(SIGSTOP)은 진행도 멈추므로 여전히 감지된다. `FFMPEG_TIMEOUT` 임계값(기본 60초)은 진행 기준으로는 보수적 여유가 크지만 회귀 위험을 피해 값을 유지한다.
7. **문서와 상태 enum 불일치 — P/P-2 도입으로 노출 증가.** 서비스 문서·메타 주석의 일부 상태 목록에는 `finalizing`이 없으나 실제로 이 상태가 존재한다. 본 스펙의 정본 enum은 §출력 `GET /api/archives`의 `status ∈ {protecting, pending, finalizing, processing, completed, failed}` 6종이다 — **enum 값 정의의 SSOT는 본 스펙이 단독 소유**한다. 이 enum을 소비하는 web-frontend의 의무(6종 전부 처리·미지 상태 안전 fallback·`failed` 노출)는 [docs/spec/interface-web-api.md](interface-web-api.md) §계약 8이 **판정 가능한 소비자 계약**으로 규정한다(값 나열은 본 스펙 참조, 소비자 의무 자체는 §계약 8 소유). **주의: 단언 P(재개→`completed`)·P-2(소실→`failed`) 계약화로 기동 복구 중 `finalizing`/`processing`/`failed` 상태가 소비자에게 노출될 확률이 증가**했으므로 §계약 8의 소비자 계약이 이를 강제한다.
8. **reload가 web-backend 조회 실패를 "카메라 0대"와 구분하지 못함.** 카메라 목록 조회가 연결 오류·본문 디코드 실패로 실패해도(HTTP 상태코드는 검사하지 않음) 오류를 반환하지 않고 빈 목록으로 reconcile하여 **모든 recorder를 중지**시키고 200 `{"status":"reloaded","cameras":0}`을 반환한다. 같은 접면을 쓰는 cctv-adapter(조회 실패 시 502/500 + 기존 push 유지)·youtube-adapter(조회 실패 시 500 + 기존 스트림 유지)와 비대칭이며, web-backend 일시 장애 중 reload 한 번으로 전체 녹화가 끊길 수 있다. 실패 시 기존 recorder 유지 + 오류 응답이 의도인지 확인 필요.
