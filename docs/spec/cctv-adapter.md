# cctv-adapter 스펙

> 상태: 살아있는 계약 (living spec) · 독자: 스펙 작성자

## 목적 / 의도

RTSP CCTV 카메라의 영상을 Sentinel streaming 서버의 RTMP 입력 규격에 맞춰 push하는 어댑터.

- 카메라 대수와 무관하게 각 카메라를 독립적으로 push하며, 한 카메라의 장애가 다른 카메라의 push에 영향을 주지 않는다.
- 소스 장애(카메라 다운, 네트워크 단절, push 프로세스 hang)로부터 **사람 개입 없이** 자동 복구한다.
- 트랜스코딩 없이(codec pass-through) 원본 스트림을 중계하여 단일 미니 PC의 CPU 자원을 보존한다.
- 어댑터 패턴의 참조 구현: 새 소스 타입 어댑터는 이 계약 구조(inbound pull → RTMP push + 헬스/상태/reload HTTP)를 따른다.
- 스트림의 alive/dead 판정 권위는 이 서비스가 아니다 — 그 SSOT는 streaming 서비스이며(`docs/spec/interface-streaming.md` §계약 4 소유), 본 서비스는 자신이 관리하는 push 프로세스의 상태만 노출한다.

## 언어 · 런타임

- Go 1.22 (표준 라이브러리만 사용, 외부 Go 의존성 없음)
- Docker 컨테이너 1개로 배포, 컨테이너 내부에서 push 프로세스(FFmpeg)를 자식 프로세스로 관리
- HTTP 서버는 컨테이너 내부 포트 8080에서 수신

## 의존 도구 · 시스템

| 의존 대상 | 관계 | 계약 소유자 |
|-----------|------|------------|
| FFmpeg 바이너리 | 컨테이너에 동봉, 카메라당 1 프로세스 실행 | — (내부 도구) |
| RTSP 카메라 | inbound pull (카메라 장비 고유 RTSP URL, TCP 전송) | 카메라 장비 |
| streaming 서비스 | outbound RTMP push | `docs/spec/interface-streaming.md` — §계약 1 (RTMP 입력) |
| web-backend | reload 시 카메라 목록 조회 (`GET {WEB_BACKEND_URL}/internal/cameras`) | `docs/spec/interface-web-api.md` — §계약 13 (Internal) (본 스펙은 소비 필드만 명시) |

## 입력

### 1. 부트 카메라 설정 파일

- 위치: `CAMERAS_CONFIG_PATH` (기본 `/config/cameras.json`), read-only mount.
- 형식: JSON 배열 `[{cameraId, name, rtspUrl}]`.
- 파일 부재·파싱 실패는 치명적이지 않다: 카메라 0대로 기동하며 서비스는 정상 동작(헬스 200)한다.

### 2. web-backend 카메라 목록 (reload 시)

- `GET {WEB_BACKEND_URL}/internal/cameras` 응답에서 다음 필드를 소비한다:
  `name`, `streamKey`, `sourceType`, `sourceUrl`, `enabled`.
- 이 중 `sourceType == "rtsp"` 이고 `enabled == true` 이며 `sourceUrl`·`streamKey`가 비어 있지 않은 항목만 push 대상이 된다. 나머지는 무시한다.
- `streamKey`가 RTMP push의 stream key(= 부트 설정의 `cameraId` 역할)로 쓰인다.

### 3. RTSP 스트림

- 각 카메라의 RTSP URL에서 pull. 전송은 TCP.
- 전제조건: 카메라 출력이 H.264 + AAC일 것 (copy 모드 pass-through의 조건 — B-frame 유무는 무관하며 허브가 수용. 입력 규격은 `docs/spec/interface-streaming.md` §계약 1 소유). 비-H.264(HEVC 등) 소스는 어댑터가 정규화 재인코딩해야 브라우저 재생 가능.

### 4. 환경변수

| 변수 | 기본값 | 의미 |
|------|--------|------|
| `CAMERAS_CONFIG_PATH` | `/config/cameras.json` | 부트 카메라 목록 파일 |
| `STREAMING_RTMP_URL` | `rtmp://streaming:1935/live` | RTMP push 베이스 URL |
| `WEB_BACKEND_URL` | `http://web-backend:8080` | reload 시 카메라 목록 조회 대상 |
| `FFMPEG_TIMEOUT` | `30` (초) | push 프로세스 진행(progress) 정지(stall) 판정 임계치 — 진행 업데이트(fd 3) 무수신 지속 시간 기준. 비정수·0 이하 값은 무시되고 기본값 적용 |

## 출력 (계약)

### 1. RTMP push (주 산출물)

- 대상: `{STREAMING_RTMP_URL}/{streamKey}` — 규격(FLV 컨테이너, H.264 + AAC)은 `docs/spec/interface-streaming.md` §계약 1 (RTMP 입력)이 소유하며(허브는 B-frame 포함 H.264도 수용), 본 서비스는 그 규격을 준수하는 push를 보장한다.
- 트랜스코딩 없음: 출력 스트림의 비디오/오디오 코덱은 입력 RTSP 스트림과 동일하다(codec copy).

### 2. HTTP API (내부 8080)

| Method | Path | 응답 계약 |
|--------|------|----------|
| GET | `/healthz` | `200` + `{"status":"ok","service":"cctv-adapter"}` — 카메라 상태와 무관하게 프로세스 생존 시 항상 200 |
| GET | `/api/cameras/status` | `200` + JSON 배열. 원소: `{cameraId, status, connectedAt, lastError}`. `status ∈ {connected, disconnected, reconnecting}`, `connectedAt`은 RFC3339 또는 null, `lastError`는 문자열 또는 null. 원소 수 = 현재 설정된 카메라 수 |
| POST | `/api/cameras/reload` | 성공: `200` + `{"status":"reloaded","cameras":N}` (N = reconcile 후 push 대상 수). web-backend 접근 실패: `502` + JSON error. 응답 파싱 실패: `500` + JSON error. 실패 시 기존 push 프로세스는 변경되지 않는다 |

### 3. 영속 상태

- 없음. 디스크에 아무것도 쓰지 않는다. 런타임 상태(프로세스 핸들·상태 맵)는 in-memory이며 재시작 시 부트 설정 파일에서 재구성된다.

## 핵심 로직 (동작)

1. **카메라별 독립 관리** — 카메라마다 독립적인 push 프로세스와 생명주기 루프를 가진다. 한 카메라의 실패·재시작이 다른 카메라의 push를 중단시키지 않는다.

2. **자동 재연결** — push 프로세스가 어떤 이유로든 종료되면 자동으로 재시작한다. 프로세스 기동 실패가 반복되면 재시도 간격은 1초에서 시작해 2배씩 증가하며 최대 30초로 상한된다. 기동 성공 시 간격은 1초로 초기화된다.

3. **진행 정지 watchdog** — liveness 신호는 push 프로세스의 **진행(progress) 스트림**이다: push 프로세스는 전용 fd(fd 3)로 구조화된 진행 정보(frame=/out_time= 등)를 약 0.5초 간격으로 내보내며, watchdog는 이 진행 업데이트의 **마지막 수신 시각**을 기준으로 한다. `FFMPEG_TIMEOUT` 초 동안 진행 업데이트가 없으면 hang으로 판정하여 종료시킨다(정상 종료 요청 후 5초 내 미종료 시 강제 종료). 종료된 프로세스는 재연결 규칙(2)에 따라 재시작된다. 로그(stdout/stderr)는 liveness 판정에 쓰지 않는다 — `-loglevel warning` + `-c copy` 정상 스트림은 로그가 거의 없어 무출력이 곧 hang이 아니기 때문이다. 프로세스가 얼어붙으면(예: SIGSTOP) 진행 방출도 멈추므로 실제 hang은 여전히 감지된다.

4. **Hot reload (reconcile)** — reload 요청 시 web-backend에서 최신 목록을 가져와 현재 실행 목록과 diff한다:
   - streamKey와 RTSP URL이 **둘 다 동일**한 카메라: push를 중단하지 않는다 (무중단).
   - 목록에서 제거되었거나 RTSP URL이 변경된 카메라: 기존 프로세스를 종료한다 (정상 종료 요청 후 최대 3초 뒤 강제 종료).
   - 새로 추가되었거나 URL이 변경된 카메라: push를 시작한다.
   - reload 후 상태 조회는 새 목록 기준으로 응답한다.

5. **상태 노출** — 각 카메라의 상태는 push 프로세스 생명주기를 반영한다: 기동 성공 시 `connected` + `connectedAt` 기록, 종료 시 `disconnected` (+ 비정상 종료면 `lastError` 기록), 재시도 대기·기동 중이면 `reconnecting`. 이는 프로세스 수준 상태이며 스트림 도달성의 권위가 아니다(SSOT는 streaming).

6. **관용적 기동** — 부트 설정이 없거나 깨져도 서비스는 기동한다(카메라 0대). 이후 reload로 카메라를 공급받을 수 있다.

## 검증 단언 (TDD)

- **A. 헬스** — 컨테이너 기동 후 `curl -s http://cctv-adapter:8080/healthz` 가 HTTP 200과 body `{"status":"ok","service":"cctv-adapter"}` 를 반환하면 OK. 카메라 0대 상태에서도 동일하게 200이면 OK.

- **B. 상태 스키마** — 카메라 N대 설정 후 `GET /api/cameras/status` 응답이: (1) JSON 배열이고 원소 수가 N, (2) 각 원소가 `cameraId`(string), `status`(`connected|disconnected|reconnecting` 중 하나), `connectedAt`(RFC3339 문자열 또는 null), `lastError`(문자열 또는 null) 를 갖으면 OK.

- **C. RTMP push 성립** — 유효한 RTSP 소스 1대(`cameraId=camX`)를 부트 설정에 넣고 기동하면, 30초 이내에 streaming의 HLS endpoint `/live/camX/index.m3u8` 가 재생 가능해지면 OK. (판정: `ffprobe <hls-url>` 성공 또는 streaming `GET /api/streams` 에서 `streamKey=camX, active=true`)

- **D. 무 트랜스코딩** — 단언 C의 상태에서 HLS 출력의 비디오 코덱/프로파일/해상도가 입력 RTSP 스트림과 동일하면 OK. (판정: `ffprobe -show_streams` 를 입력/출력 양쪽에 실행해 `codec_name`, `width`, `height` 일치 확인)

- **E. 부트 설정 관용성** — 설정 파일을 제거(또는 깨진 JSON으로 교체)하고 기동해도 프로세스가 살아 있고 `GET /healthz` 200, `GET /api/cameras/status` 가 빈 배열 `[]` 을 반환하면 OK.

- **F. reload 성공 경로** — web-backend 목록이 `[{name:"A", streamKey:"cam-a", sourceType:"rtsp", sourceUrl:"rtsp://...", enabled:true}, {streamKey:"cam-b", sourceType:"youtube", enabled:true, ...}, {streamKey:"cam-c", sourceType:"rtsp", enabled:false, ...}]` 일 때 `POST /api/cameras/reload` 가 `200 {"status":"reloaded","cameras":1}` 을 반환하고, 이후 status 응답에 `cam-a` 만 존재하면 OK. (rtsp + enabled + 비어있지 않은 sourceUrl/streamKey 만 채택)

- **G. reload 실패 경로** — web-backend가 접근 불가일 때 `POST /api/cameras/reload` 가 HTTP 502 + JSON error body를 반환하고, 기존 카메라의 `status`/`connectedAt` 이 reload 시도 전과 동일하게 유지되면 OK.

- **H. reconcile 무중단** — 카메라 `cam-a` 가 push 중일 때, 동일한 `streamKey + sourceUrl` 로 reload를 실행해도 `cam-a` 의 `connectedAt` 이 변하지 않으면(프로세스 미중단) OK. 반대로 `sourceUrl` 을 바꿔 reload하면 `connectedAt` 이 갱신되고(재시작) 새 URL로 push되면 OK. 목록에서 `cam-a` 를 빼고 reload하면 status 응답에서 사라지고 RTMP push가 중단되면 OK.

- **I. 자동 재연결** — 단언 C의 상태에서 RTSP 소스를 강제 중단 후 재개하면, 사람 개입 없이 120초 이내에 HLS가 다시 재생 가능(`active=true`)해지면 OK.

- **J. watchdog** — push 프로세스가 `FFMPEG_TIMEOUT` 초 이상 진행(progress) 업데이트 없이(fd 3 무수신) 유지될 때, `1.5 × FFMPEG_TIMEOUT + 15초` 이내에 해당 프로세스가 종료되고 새 프로세스로 교체되면(프로세스 PID 변경 관측) OK. (시한 근거: watchdog 검사는 `FFMPEG_TIMEOUT/2` 주기이므로 hang 감지가 최대 1.5×timeout까지 지연되고, 종료는 SIGTERM 후 5초 유예를 거친다 — 기본 30초 설정에서 최악 약 50초.) 테스트는 push 프로세스를 SIGSTOP으로 완전 동결시켜 진행 방출을 멈추고 hang을 유발한다 — 정상 무출력(로그 침묵)과 달리 동결은 진행도 멈추므로 감지된다.

- **K. 환경변수 기본값** — 네 변수 모두 미설정으로 기동해도 기본값(`/config/cameras.json`, `rtmp://streaming:1935/live`, `http://web-backend:8080`, 30초)으로 동작하면 OK. `FFMPEG_TIMEOUT=abc` 또는 `0` 설정 시 기동 로그에 경고가 남고 30초 기본값으로 동작하면 OK.

## ⚠️ 리뷰 필요 (의도 불확실)

스펙 본문에 넣지 않은, 구현에서 관찰되었으나 의도인지 불확실한 동작들. 각각 확정 후 본문 반영 또는 수정 대상.

1. ~~**정상 스트리밍 중 watchdog 오탐 가능성**~~ — **[해결됨 · #68, B안 확정]** liveness 신호를 로그 출력(stdout/stderr) 유무에서 push 프로세스의 **진행(progress) 스트림(fd 3)** 으로 전환하여 본문(핵심 로직 #3, 단언 J)에 승격했다. `-loglevel warning` + `-c copy` 정상 스트림은 로그가 침묵해도 진행 업데이트를 계속 방출하므로 더 이상 hang으로 오판하지 않으며, 실제 동결(SIGSTOP)은 진행도 멈추므로 여전히 감지된다. `FFMPEG_TIMEOUT` 임계값은 진행 기준으로는 보수적 여유가 크지만 회귀 위험을 피해 30초 기본값을 유지한다.

2. **`connected` 상태의 의미** — 상태가 `connected` 로 바뀌는 시점은 push 프로세스의 기동 성공이며, 실제 RTSP 연결·RTMP push 성립을 의미하지 않는다. 존재하지 않는 RTSP URL이어도 몇 초간 `connected` 로 보인다. 프로세스 수준 상태로 의도된 것인지, 스트림 성립 상태로 보이길 의도한 것인지 확인 필요. (본 스펙은 전자로 기술함)

3. **접속 실패 카메라에 지수 백오프 미적용** — 백오프는 "프로세스 기동 성공" 시 1초로 초기화되는데, 도달 불가 RTSP URL이라도 FFmpeg 프로세스 자체는 정상 기동한 뒤 종료한다. 따라서 연결 실패 카메라는 백오프가 매번 초기화되어 사실상 1초 간격으로 무한 재시도한다. 서비스 가이드의 "clean exit 시 backoff reset" 서술과도 다르다. 의도(빠른 복구 우선) 여부 확인 필요.

4. **서비스 가이드와 코드의 web-backend 응답 스키마 불일치** — 가이드는 `[{id, streamKey, rtspUrl, enabled}]` 라고 기술하나, 구현이 소비하는 필드는 `{name, streamKey, sourceType, sourceUrl, enabled}` 다. 어느 쪽이 계약인지 확정 필요. (본 스펙은 구현이 소비하는 필드 기준으로 기술함)

5. **컨테이너 종료 시그널 미처리** — SIGTERM/SIGINT 핸들링이 없어 컨테이너 정지 시 push 프로세스들의 정돈된 종료(전체 SIGTERM → 5초 → SIGKILL 경로)가 실행되지 않는다. 정돈된 종료 경로는 HTTP 서버 기동 실패 시에만 도달한다. graceful shutdown이 요구사항인지 확인 필요.

6. **status 응답 필드가 서비스 가이드 서술과 상이** — 가이드는 "running / restart count / last error" 를 언급하나 실제 응답에는 restart count가 없고 상태 값도 `connected/disconnected/reconnecting` 이다. 가이드 갱신 또는 필드 추가 중 어느 쪽이 의도인지 확인 필요. (본 스펙은 실제 응답 기준)
