# youtube-adapter 스펙

> 상태: 살아있는 계약 (living spec) · 독자: 스펙 작성자

## 목적 / 의도

YouTube 영상 URL 또는 로컬 비디오 파일을 소스로 삼아, streaming 서비스의 RTMP 입력 규격에 맞는 영상 스트림을 지속적으로 push하는 것을 보장하는 demo/test 어댑터다. 실제 CCTV 없이도 시스템 전체(streaming → HLS → web)의 영상 파이프라인을 구동·시연할 수 있게 하는 것이 존재 이유다. 소스 코덱·비트레이트가 제각각이라 항상 재인코딩으로 저지연 저비트레이트 H.264로 **정규화**한다는 점이 계약의 핵심이다(코덱 정규화 = 어댑터 책임 — 허브는 트랜스코딩하지 않음). 허브가 B-frame을 거부해서가 아니라 소스 정규화를 위한 재인코딩이다.

## 언어 · 런타임

- Go 단일 바이너리, Docker 컨테이너로 구동 (컨테이너 내부 포트 8080).
- 컨테이너 이미지에 FFmpeg와 yt-dlp가 포함되어야 한다.

## 의존 도구 · 시스템

- **FFmpeg** — 소스 재생·재인코딩·RTMP push 실행기.
- **yt-dlp** — YouTube URL을 직접 재생 가능한 스트림 URL로 해석.
- **streaming 서비스** — RTMP 수신자. 접면 계약의 소유자는 `docs/spec/interface-streaming.md` §계약 1 (RTMP 입력). 본 스펙은 그 규격을 재정의하지 않고 준수만 보장한다.
- **web-backend** — reload 시 카메라 목록의 원천 (`GET /internal/cameras`). 이 API의 계약 소유자는 `docs/spec/interface-web-api.md` §계약 13 (Internal).
- **설정 파일** — JSON 소스 목록 (read-only). **로컬 미디어 파일** — `/media` read-only 마운트.

## 입력

- **환경변수** (모두 기본값 보유 — 미설정 시에도 기동):
  - `YOUTUBE_CONFIG_PATH` (기본 `/config/youtube-sources.json`)
  - `STREAMING_RTMP_URL` (기본 `rtmp://streaming:1935/live`)
  - `WEB_BACKEND_URL` (기본 `http://web-backend:8080`)
- **설정 파일**: JSON 배열. 각 원소 = `{id, youtubeUrl, streamKey, localFile?}`.
  - `localFile`이 있으면 로컬 파일이 소스가 된다. 없으면 `youtubeUrl`을 yt-dlp로 해석한다.
- **YouTube URL 제약**: https 필수, 최대 200자, `youtube.com/watch?v=…`, `youtube.com/live/…`, `youtu.be/…` 형태만 허용. 이를 위반하는 소스는 기동 시 건너뛰고(경고 로그) 나머지 소스는 정상 기동한다.
- **HTTP 요청**: `GET /healthz`, `GET /api/streams/status`, `POST /api/cameras/reload`.
- **reload 시 인바운드 데이터**: web-backend의 카메라 목록(각 항목에 `streamKey`, `sourceType`, `sourceUrl`, `enabled` 포함).

## 출력 (계약)

- **RTMP push** — 소스마다 `{RTMP 베이스}/{streamKey}`로 지속 송출. 스트림 내용 규격(FLV 컨테이너, H.264, AAC 오디오, streamKey 규칙)의 소유자는 `docs/spec/interface-streaming.md` §계약 1이며(허브는 B-frame 포함 H.264도 수용) 본 서비스는 그 규격을 항상 만족하는 스트림만 내보낸다. 로컬 파일 소스는 파일 끝에 도달해도 끊기지 않고 무한 반복 송출된다.
- **`GET /healthz`** — 200, JSON `{"status":"ok","service":"youtube-adapter"}`. 스트림 상태와 무관하게 프로세스 생존만 나타낸다.
- **`GET /api/streams/status`** — 200, JSON 배열. 현재 소스 목록의 각 소스당 1개 원소: `{id, streamKey, status, lastError?, startedAt?, loopCount}`. `status ∈ {starting, running, error, stopped, unknown}`.
- **`POST /api/cameras/reload`** — 성공 시 200 `{"status":"ok","count":N}` (N = 채택된 YouTube 소스 수). web-backend 조회 실패 시 500 `{"error":…}`이며 기존 스트림들은 그대로 유지된다.
- **영구 상태 없음** — 재시작 시 설정 파일만으로 동일 동작이 재현된다. 디스크에 쓰지 않는다.

## 핵심 로직 (동작)

- **소스 선택 규칙**: 소스에 `localFile`이 있으면 yt-dlp를 사용하지 않고 그 파일을 재생한다. 없으면 yt-dlp로 URL을 해석하되, 해석은 30초 안에 끝나야 하며 실패는 해당 소스만의 error로 격리된다.
- **재인코딩 필수(정규화)**: 어떤 소스든 출력은 항상 H.264 + AAC로 재인코딩된다(저비트레이트·저지연 정규화). 소스 코덱을 그대로 통과(copy)시키지 않는다. `-preset ultrafast`의 부작용으로 결과 스트림에 B-frame이 없으나, 이는 저지연 최적화의 부산물이지 허브 수락 조건이 아니다(허브는 B-frame을 수용).
- **격리와 자가 복구**: 소스별로 독립 관리된다. 한 소스의 실패(yt-dlp 실패, FFmpeg 비정상 종료)는 다른 소스와 HTTP API에 영향을 주지 않는다. 실패한 소스는 지수 backoff(1s 시작, 상한 30s)로 무한 재시도하고, 정상 종료 후 재시작 시 backoff는 1s로 리셋된다. YouTube URL 소스는 스트림이 정상 종료되면 URL을 다시 해석해 재시작한다(항상 켜져 있음을 지향).
- **reload 재조정(reconcile)**: reload 요청 시 web-backend 카메라 목록에서 `sourceType=youtube`이고 `enabled=true`이며 `streamKey`가 비어있지 않은 항목만 채택해 새 소스 집합으로 삼는다. 기존과 동일한(같은 식별자 + 같은 URL) 스트림은 중단 없이 계속 돌고, URL이 바뀌었거나 목록에서 사라진 스트림은 중지되며, 새 항목은 시작된다.
- **설정 결함 내성**: 설정 파일이 없거나 파싱 불가여도 서비스는 스트림 0개로 기동한다(헬스 정상). 유효하지 않은 URL의 소스는 개별 건너뛴다.
- **정상 종료**: SIGTERM/SIGINT 수신 시 모든 FFmpeg 프로세스에 종료를 지시한 뒤 내려간다. 강제 종료 전 유예를 둔다.

## 검증 단언 (TDD)

- A. **헬스**: 컨테이너 기동 후 `curl -s http://youtube-adapter:8080/healthz` → HTTP 200, body가 `{"status":"ok","service":"youtube-adapter"}` 이면 OK.
- B. **상태 조회 형태**: 소스 2개짜리 config로 기동 후 `curl -s http://youtube-adapter:8080/api/streams/status` → HTTP 200, 길이 2의 JSON 배열, 각 원소에 `id`·`streamKey`·`status`·`loopCount` 존재, `status`가 `{starting,running,error,stopped,unknown}` 중 하나면 OK.
- C. **URL 검증 입출력 쌍**: 다음 URL을 담은 소스로 기동했을 때 —
  - 채택(스트림 생성): `https://www.youtube.com/watch?v=abc123`, `https://youtu.be/abc123`, `https://youtube.com/live/abc123`
  - 거부(경고 후 skip, status 목록에 미등장): `http://youtube.com/watch?v=abc`(스킴), 201자 이상 URL, `https://example.com/watch?v=abc`(도메인), `https://youtube.com.evil.com/watch?v=abc`(도메인 위장)
  — 위 판정이 전부 일치하면 OK.
- D. **로컬 파일 무한 반복**: 10초짜리 `/media/test.mp4`를 `localFile`로 지정하고 기동 → 60초 이상 경과 후에도 streaming의 `GET /api/streams`에서 해당 `streamKey`가 `active: true`이고, yt-dlp 프로세스가 한 번도 실행되지 않았으면 OK.
- E. **RTMP 출력 규격 준수**: 송출 중인 스트림의 결과물(HLS 세그먼트 또는 RTMP 구독)을 ffprobe로 검사 → video codec `h264`, audio codec `aac`이고, push 시작 후 60초 이상 연결이 유지되면 OK. (규격 자체의 정의는 `docs/spec/interface-streaming.md` §계약 1이 소유. B-frame 유무는 판정 대상이 아니다 — 허브가 수용하므로. 참고: 현 구현은 `-preset ultrafast` 부산물로 `has_b_frames=0`이나 이는 계약 요구가 아니라 부수적 성질이다.)
- F. **장애 격리·재시도**: 존재하지 않는 영상 URL 소스 1개 + 정상 로컬 파일 소스 1개로 기동 → 30초 내 status에서 전자는 `status:"error"`+`lastError` 비어있지 않음, 후자는 `running`, `/healthz`는 계속 200이면 OK. 이후 관측되는 재시도 간격이 단조 증가하되 30초를 넘지 않으면 OK.
- G. **reload 재조정**: (1) web-backend가 유효 YouTube 카메라 N개를 반환하는 상태에서 `curl -s -X POST http://youtube-adapter:8080/api/cameras/reload` → 200 `{"status":"ok","count":N}`, 이후 status 목록이 그 N개와 일치하면 OK. (2) `sourceType`이 youtube가 아니거나 `enabled=false`이거나 `streamKey`가 빈 카메라는 count와 목록에서 제외되면 OK. (3) 동일 streamKey·동일 URL 카메라는 reload 전후 `startedAt`이 변하지 않으면(재시작 안 됨) OK. (4) web-backend가 다운된 상태에서 reload → 500 응답이고 기존 스트림의 status·`startedAt`이 그대로면 OK.
- H. **설정 결함 내성**: config 파일 없이(또는 깨진 JSON으로) 기동 → 컨테이너가 크래시하지 않고 `/healthz` 200, `/api/streams/status`가 빈 배열이면 OK.
- I. **정상 종료**: 스트림 1개 송출 중 컨테이너에 SIGTERM → 컨테이너 종료 후 호스트/컨테이너에 잔존 ffmpeg 프로세스가 없으면 OK.

## ⚠️ 리뷰 필요 (의도 불확실)

스펙 본문에 넣지 않은, 의도인지 불확실한 관찰 동작들. 확인 후 스펙 본문 승격 또는 수정 결정 필요.

1. **reload가 config 파일 기반 소스를 전면 폐기함** — reload는 web-backend 목록으로 소스 집합을 통째로 교체하며, web-backend 경유 소스에는 `localFile` 개념이 없다. 따라서 config 파일로 띄운 로컬 파일 스트림(권장 모드)도 reload 한 번이면 전부 중지된다. "config 파일과 web-backend 중 무엇이 SSOT인가"가 미정의.
2. **localFile 전용 소스가 기동 시 탈락함** — config 로드 시 모든 소스에 대해 `youtubeUrl` 유효성을 검사해 실패 시 skip한다. `localFile`만 있고 `youtubeUrl`이 빈 소스는 로컬 파일 재생이 가능함에도 거부된다. 서비스 문서의 "localFile 있으면 파일 재생(권장)"과 긴장 관계.
3. **reload의 '변경 없음' 판정이 URL만 비교** — 같은 식별자에서 `localFile`이 바뀌어도 변경으로 감지되지 않는다(현재 reload 경로가 localFile을 만들지 못해 잠재적 dead path이나, 1번 해소 방식에 따라 실동작이 됨).
4. **출력 H.264 profile 미고정** — FFmpeg 인자에 `-profile:v`가 없어 profile은 preset의 부작용으로 결정된다(현재 `-preset ultrafast`가 B-frame·CABAC을 끄므로 사실상 Constrained Baseline 송출). 허브는 profile·B-frame을 강제하지 않으므로(§계약 1, "설계 결정 기록") **이는 허브 계약 문제가 아니다.** 남는 질문은 본 어댑터가 자기 출력 일관성(preset 변경 시 profile 무단 변화 방지)을 위해 `-profile:v`를 pin할지의 어댑터 내부 선택뿐 — 필요 시 여기서 결정한다.
5. **localFile 경로가 서비스 내부에서 검증되지 않음** — 서비스 문서는 "`/media/` 하위로 제한"이라 하나, 제한은 compose의 read-only 마운트에만 의존하고 코드 차원의 경로 검증은 없다.
6. **종료 시 FFmpeg 유예 완료를 기다리지 않을 수 있음** — 시그널 수신 시 각 스트림에 중지를 지시한 직후 프로세스를 종료한다. FFmpeg SIGTERM→5초 유예→KILL 시퀀스가 완료되기 전에 메인 프로세스가 exit할 수 있다(컨테이너 환경에선 PID 1 종료로 함께 정리되지만, 단언 I의 엄밀성에 영향).
7. **허용 URL 형태의 문서-코드 불일치** — 서비스 문서는 `youtube.com/watch`와 `youtu.be`만 허용이라 기술하나, 실제로는 `youtube.com/live/…`(라이브 스트림)도 허용된다. 라이브 허용이 의도라면 문서 갱신 필요.
