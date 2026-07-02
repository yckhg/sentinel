# recording 스펙

> 상태: 살아있는 계약 (living spec) · 독자: 스펙 작성자

## 목적 / 의도

- 모든 활성 카메라의 영상을 **끊김 없이 상시 녹화**하여, 위기(incident) 발생 시 "사건 전 1시간 ~ 상황 종료 후 30분" 구간의 증거 영상을 반드시 확보할 수 있게 한다.
- 저장 공간은 유한하므로, 보호되지 않은 녹화분은 **롤링 윈도우**(기본 60분)만 유지하고 자동 삭제한다.
- incident 영상은 두 단계 계약(**protect → finalize**)으로 보존한다: 위기 감지 즉시 삭제만 막고(protect), 상황 종료 후 하나의 MP4로 병합해 영구 아카이브를 만든다(finalize).
- 과거 구간을 브라우저에서 즉시 돌려볼 수 있도록, 저장된 세그먼트로부터 **VOD형 HLS playlist를 동적 생성**한다.
- 전 구간 **무 트랜스코딩**(`-c copy`) — 녹화·병합 모두 재인코딩하지 않는다 (mini PC CPU 보호).

## 언어 · 런타임

- Go 1.22, 표준 라이브러리만 사용 (외부 Go 의존성 0).
- 단일 정적 바이너리로 빌드되어 Docker 컨테이너(`sentinel-ffmpeg-base` 기반)에서 실행된다.
- 내부 HTTP 포트 8080.

## 의존 도구 · 시스템

- **FFmpeg** (컨테이너 내 필수): RTMP 수신·세그먼트 분할, 세그먼트 병합(concat demuxer) 모두 FFmpeg 하위 프로세스로 수행한다.
- **streaming 서비스** (RTMP pull 원천): 계약 소유자는 [docs/interfaces/streaming-api.md](../interfaces/streaming-api.md). 본 스펙은 그 RTMP 규격(코덱, B-frame 금지 등)을 재정의하지 않는다.
- **web-backend**: 카메라 목록 원천(`GET /internal/cameras`) 및 본 서비스 `/api/*`의 인증 프록시. 이 접면의 외부 노출 계약은 web-backend 측 인터페이스 문서가 소유한다.
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
  - 파일명은 **UTC** 타임스탬프, 10초 단위, 시계 시각 정렬(segment_atclocktime), MPEG-TS 컨테이너, 코덱 원본 유지.
  - 보호되지 않은 세그먼트는 파일명 타임스탬프(파싱 불가 시 mtime) 기준으로 롤링 윈도우 초과 시 삭제된다.
  - 0바이트 세그먼트는 보호 여부와 무관하게 삭제된다.
- **영구 아카이브**: `{ARCHIVES_DIR}/{archiveId}/{streamKey}.mp4`
  - `archiveId = {incidentId}_{streamKey}_{fromUTC(YYYYMMDD_HHMMSS)}` — 같은 (incident, streamKey, from)에 대한 중복 생성 요청은 기존 항목을 반환하며 새로 만들지 않는다.
  - MP4는 `+faststart` 적용(스트리밍 재생 가능), 세그먼트들의 무손실 병합본이다.
- **아카이브 메타데이터**: `{ARCHIVES_DIR}/metadata.json` — 전체 아카이브 목록의 SSOT. 상태 변화마다 저장되고 재시작 시 로드된다.

### HTTP API (응답 계약 요지)

| Method·Path | 보장 |
|---|---|
| `GET /healthz` | 200, `{"status":"ok","service":"recording"}` |
| `GET /api/status` | 활성 카메라별 `{streamKey, status, startedAt, lastError, segmentDir}` 배열. `status ∈ {recording, reconnecting, disconnected}` |
| `POST /api/cameras/reload` | 최신 카메라 목록으로 recorder를 reconcile(추가된 카메라 시작, 제거된 카메라 중지). `{"status":"reloaded","cameras":N}` |
| `GET /api/recordings/{key}` | 사용 가능 구간 `{streamKey, timeRanges:[{start,end}]}` (RFC3339). 세그먼트 간격 15초 이하면 하나의 연속 구간으로 병합. 녹화 없으면 404 |
| `GET /api/recordings/{key}/play?from=&to=` | `application/vnd.apple.mpegurl`, VOD형 playlist(`EXT-X-PLAYLIST-TYPE:VOD` + `EXT-X-ENDLIST`), 구간과 겹치는 세그먼트를 시간순 나열. 해당 세그먼트 없으면 404. `from`/`to` 누락·형식 오류는 400 |
| `GET /api/recordings/{key}/segments/{file}` | `.ts` 파일을 `video/mp2t`로 서빙. 경로 탈출(`/`, `\`, `..`) 및 `.ts` 외 확장자는 400 |
| `POST /api/archives/protect` | (Phase 1) 202. incident별·streamKey별 상태 `protecting` 아카이브 항목 생성. `incidentTime − 1시간`부터의 세그먼트를 삭제 보호. 이후 도착하는 세그먼트도 주기적으로 계속 보호 |
| `POST /api/archives/finalize` | (Phase 2) 해당 incident의 `protecting` 아카이브들을 `resolvedAt + 30분`을 종료 시각으로 백그라운드 병합 시작. `protecting`이 하나도 없으면 404 |
| `POST /api/archives` | 임의 구간 즉시 아카이브(수동). 202 + 생성된 항목들. `incidentId` 생략 시 `manual_*` 자동 부여 |
| `GET /api/archives` | 전체 아카이브 메타 배열. `status ∈ {protecting, pending, finalizing, processing, completed, failed}` |
| `GET /api/archives/{id}/download` | `completed`만 `video/mp4` + attachment로 서빙. 미완료면 409, 없으면 404 |
| `DELETE /api/archives/{id}` | 아카이브 디렉터리·메타 삭제. 없으면 404 |
| `DELETE /api/archives/incident/{incidentId}` | 해당 incident의 모든 아카이브 삭제 + 삭제 수 반환. 없으면 404 |
| `GET /api/storage` | `{recordingsBytes, archivesBytes, totalUsedBytes, archiveCount, diskTotalBytes, diskUsedBytes, diskAvailableBytes}` |

- playlist가 가리키는 세그먼트 URL(`/api/recordings/...`)의 외부 노출 형태·인증은 web-backend 프록시 계약(web-backend 인터페이스 문서 소유)을 따른다.

## 핵심 로직 (동작)

1. **상시 녹화 루프 (카메라당 1개)**
   - FFmpeg로 RTMP를 pull하여 무 트랜스코딩으로 10초 `.ts` 세그먼트를 쓴다.
   - 연결 실패/종료 시 exponential backoff **1초 → 최대 30초**로 무한 재시도한다. 성공적으로 시작되면 backoff는 1초로 리셋된다.
   - 상태 전이: 시작 시도 중 `reconnecting` → 프로세스 기동 성공 `recording` → 종료/실패 `disconnected`(+ `lastError` 기록).
2. **Watchdog**: FFmpeg의 stdout/stderr 출력이 `FFMPEG_TIMEOUT`초 동안 없으면 stall로 간주, SIGTERM → 5초 후 SIGKILL로 종료시키고 재연결 루프에 맡긴다.
3. **롤링 정리 (30초 주기)**: 모든 스트림 디렉터리에서 (a) 0바이트 `.ts`는 무조건 삭제, (b) 미보호이면서 롤링 윈도우보다 오래된 `.ts`를 삭제한다. 보호 목록은 in-memory 파일 경로 집합이다.
4. **incident 2단계 아카이브**
   - **protect**: `incidentTime − 1시간 ~ 현재`의 기존 세그먼트를 보호 집합에 넣고, streamKey별 `protecting` 메타를 만든다.
   - **보호 갱신 (30초 주기)**: `protecting` 상태인 동안 새로 생기는 세그먼트도 계속 보호 집합에 추가된다 — 즉 protect 이후 도착 영상도 삭제되지 않는다.
   - **finalize**: 종료 시각을 `resolvedAt + 30분`으로 확정하고, 구간 내 세그먼트를 보호 처리 후 FFmpeg concat(`-c copy`)으로 단일 MP4를 백그라운드 생성한다. 성공 시 `completed`(+파일 크기), 실패 시 `failed`(+사유)로 기록된다.
   - **자동 finalize (30초 주기)**: `protecting` 상태가 incident 발생 후 **2시간**을 넘기면 finalize 누락으로 간주하고 현재 시각 기준으로 자동 finalize한다 — 보호가 무한히 지속되어 디스크가 차는 것을 막는 안전장치.
5. **pseudo-playback**: 요청 구간 `[from, to)`와 **겹치는**(경계 포함 판정: 세그먼트 종료 > from AND 세그먼트 시작 < to) 세그먼트를 시간순으로 나열한 VOD playlist를 매 요청마다 동적 생성한다. 세그먼트 길이는 10초로 간주한다.
6. **reconcile**: reload 시 streamKey 기준으로만 비교한다 — 새 key는 녹화 시작, 사라진 key는 SIGTERM(3초 후 SIGKILL)으로 중지. 동일 key의 다른 속성 변경은 녹화에 영향 없다.
7. **재시작 내성**: 메타데이터 JSON은 재시작 후 로드된다. `protecting` 아카이브는 보호 갱신 주기에 의해 재시작 후에도 세그먼트 보호가 복원된다.

## 검증 단언 (TDD)

각 단언은 컨테이너 기동 상태에서 OK/NOK 판정 가능해야 한다. (`$REC` = recording 컨테이너 내부 또는 내부망에서 `http://recording:8080`)

- **A. 헬스**: `curl -fsS $REC/healthz` 가 200이고 body가 `{"status":"ok","service":"recording"}` 이다.
- **B. 세그먼트 생성**: 활성 RTMP 스트림 `k`가 존재할 때, 30초 대기 후 `{RECORDINGS_DIR}/k/` 안에 정규식 `^\d{8}_\d{6}\.ts$` 파일이 2개 이상 새로 생기고, 최신 파일의 파일명 타임스탬프(UTC)와 현재 UTC의 차가 30초 이내다.
- **C. 상태 보고**: 단언 B 상황에서 `GET /api/status` 응답 중 `k` 항목의 `status == "recording"` 이고 `startedAt`이 RFC3339다. 스트림 발행을 중단하면 `FFMPEG_TIMEOUT + 10초` 이내에 `status`가 `reconnecting` 또는 `disconnected`로 바뀐다 (로그에 `FFmpeg output timeout` 또는 `FFmpeg exited` 존재).
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

## ⚠️ 리뷰 필요 (의도 불확실)

1. **아카이브 삭제 시 세그먼트 보호가 해제되지 않음.** 삭제 흐름이 "디렉터리 삭제 → 보호 해제 판단 → 목록에서 제거" 순서인데, 보호 해제 판단 시점에 삭제 대상 아카이브 자신이 아직 목록에 남아 있고 제외 ID를 지정하지 않아, "다른 아카이브가 참조 중"으로 항상 판정된다. 결과적으로 DELETE 후에도 원본 `.ts`가 계속 보호되어 재시작 전까지 롤링 삭제에서 제외된다(디스크 점유 지속). 의도된 보수적 동작인지, 해제 로직의 결함인지 확인 필요.
2. **finalize의 "+30분 post-roll"이 실제 MP4에 담기지 않을 수 있음.** finalize는 종료 시각을 `resolvedAt + 30분`으로 기록하지만 병합은 즉시 실행되므로, resolvedAt이 현재 시각이면 그 이후 도착할 30분치 세그먼트는 디스크에 없어서 병합에 포함되지 않는다. 메타의 `To`와 실제 영상 범위가 달라진다. post-roll이 의도라면 병합을 지연하거나 재병합이 필요하고, 아니면 `To` 기록이 과대표기다.
3. **0바이트 세그먼트를 보호 여부와 무관하게 삭제.** FFmpeg가 세그먼트를 막 연 직후(순간적으로 0바이트)인 현재 진행 중 파일이 cleanup 주기와 겹치면 기록 중 파일이 삭제될 수 있는 경쟁 창이 있다. "빈 파일은 증거 가치가 없다"는 의도로 보이나(US-003), 진행 중 파일 제외(예: mtime 최근 N초 제외) 없이 무조건 삭제하는 것이 의도인지 확인 필요.
4. **재시작 시 진행 중(`pending`/`processing`/`finalizing`) 아카이브가 영구히 그 상태로 남음.** 병합은 in-process goroutine이므로 재시작하면 이어서 실행되지 않고, 상태를 복구·재시도하는 로직이 없다. `protecting`만 주기 갱신으로 복원된다. 미완료 아카이브의 재시도/실패 처리 정책이 필요해 보인다.
5. **타임존 없는 시간 형식(`"2006-01-02 15:04:05"`)을 UTC로 해석.** protect/finalize의 fallback 파싱이 UTC 가정이라, 호출자가 로컬(KST) 시각을 보내면 보호 구간이 9시간 어긋난다. 이 fallback을 쓰는 호출자가 UTC를 보내는 것이 계약인지 확인 필요.
6. **watchdog이 "출력 없음 = 고장"을 전제.** FFmpeg를 `-loglevel warning`으로 실행하므로 완전히 정상인 프로세스가 `FFMPEG_TIMEOUT`(기본 60초) 동안 아무 출력도 내지 않으면 건강한 녹화가 주기적으로 강제 재시작될 수 있다(재시작 순간 수 초 유실). 실운영에서 FFmpeg의 주기적 stderr 출력(진행 stats 등)에 의존하는 구조가 의도인지 확인 필요.
7. **문서와 상태 enum 불일치.** 서비스 문서·메타 주석의 상태 목록에는 `finalizing`이 없으나 실제로 이 상태가 존재한다. 소비자(web-backend/프론트)가 미지의 상태를 안전하게 처리하는지 확인 필요.
