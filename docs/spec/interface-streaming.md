# 스트리밍 인터페이스 스펙

> 상태: 살아있는 계약 (living spec) · 독자: 스펙 작성자 / 오케스트레이터
>
> 이 문서는 Sentinel 스트리밍 접합부(seam)의 SSOT입니다. 개별 서비스 스펙은 이 문서의 계약을 참조하며, 계약 변경 시 관련 서비스 스펙·코드와 같은 커밋에서 수정합니다. (`docs/interfaces/streaming-api.md`를 계약 + 검증 단언 형태로 승격한 문서)

## 목적 / 의도

- **단일 수신·단일 송출**: 모든 영상 소스는 RTMP push로 streaming 서비스에 모이고, 클라이언트는 HLS로만 시청한다. 소스(카메라/어댑터)와 시청자(웹) 사이의 결합을 이 접합부 하나로 끊는다.
- **무 트랜스코딩 (remux only)**: streaming 서비스는 코덱을 건드리지 않는다. FLV→TS 컨테이너 변환만 수행하여 mini PC 한 대에서 다중 스트림을 처리한다.
- **벤더 독립성**: 이 스펙만 준수하면 어떤 카메라/어댑터든 교체 가능. streaming·web-backend는 변경 없이 유지된다.
- **상태 권위 단일화**: 스트림 alive/dead 판정의 유일한 권위는 streaming 서비스. 어댑터·타 서비스는 스트림 상태를 보고하지 않는다.

## 언어 · 런타임

| 역할 | 기술 |
|------|------|
| RTMP 수신 / HLS 생성·서빙 | nginx + ngx_rtmp_module (v1.2.2) |
| 상태 API (`/api/streams`, `/healthz`) | Go (표준 라이브러리 net/http) |
| 배포 형태 | 단일 Docker 컨테이너 (프로젝트 내부 네트워크 `sentinel-net` 전용 — 런타임 이름 `sentinel_sentinel-net`. 외부 공유 네트워크 `yc-network`에는 속하지 않으며, yc-network에서 호스트명 `streaming`은 해석되지 않는다), 메모리 상한 256M |

외부 통합자 관점에서는 **RTMP 1935 포트, HTTP 8080 포트** 두 개만 존재한다. (컨테이너 내부의 프로세스 분리는 계약 대상이 아님)

## 의존 도구 · 시스템

- **FFmpeg** — 어댑터의 RTMP push, recording의 RTMP pull, 검증 단언 실행(ffprobe)에 사용
- **hls.js** — 브라우저 측 HLS 재생 (web-frontend)
- **Docker 내부 네트워크 (`sentinel-net`)** — RTMP/HTTP 모두 내부 전용. 외부 노출은 web-frontend nginx가 `/live/`를 streaming:8080으로 프록시하는 경로뿐 (web-frontend만 `yc-network`에 추가로 조인)
- **호스트 이름 계약**: 컨테이너 이름 `streaming`이 주소의 일부다 (`rtmp://streaming:1935/...`, `http://streaming:8080/...`)

---

## 계약 1: RTMP 입력 (어댑터/카메라 → streaming)

### 입력

| 항목 | 요구사항 |
|------|----------|
| Protocol | RTMP (push) |
| Endpoint | `rtmp://streaming:1935/live/{streamKey}` (내부 네트워크 전용) |
| Container | FLV (`-f flv`) |
| Video codec | H.264 — profile은 강제되지 않음 (B-frame 없는 스트림이면 수락. Baseline 권장 — ⚠️ 리뷰 필요 5 참조) |
| Audio codec | AAC (LC, HE-AAC 등 모든 profile 허용) |
| **B-frames** | **금지** — nginx-rtmp v1.2.2가 B-frame composition time offset 포함 FLV를 ~5초 후 연결 종료 |
| **키프레임 간격** | **최대 2초 요구** — streaming은 키프레임에서만 HLS fragment를 자르므로(계약 2), 소스 키프레임 간격이 2초를 넘으면 세그먼트 길이·초기 재생 지연이 그만큼 늘어난다. 위반해도 push는 수락되지만(연결 거부 없음) 계약 2의 fragment/지연 수치는 보장되지 않는다. 재인코딩 시 `-g <2×fps>` 지정 (⚠️ 리뷰 필요 6 참조) |
| streamKey | 영숫자/하이픈 권장, 슬래시 금지. 형식은 강제되지 않음 — `cam-{8hex}`는 web-backend가 카메라 생성 시 발급하는 **생성 기본값(권장)**이며, 다른 형식(예: `yt-cam-1`)도 실존·수락된다 |

### 출력 (계약)

- push가 수락되면 streaming이 해당 streamKey의 HLS 스트림(계약 2)과 상태 항목(계약 4)을 자동 생성한다. 어댑터가 추가로 할 일은 없다.
- push 등록 API 없음 — RTMP 연결 자체가 등록이다.

### 핵심 로직 (동작 — 불변식)

- **B-frame 불변식**: 소스에 B-frame 포함 가능성이 있으면 push 측에서 `-tune zerolatency` 또는 `-bf 0`으로 제거해야 한다. 근거는 입력 표의 B-frame 금지 조항(nginx-rtmp v1.2.2가 B-frame 포함 FLV 연결을 조기 종료) — push가 지속되려면 이 불변식이 소스 측에서 보장되어야 한다.
- **copy 우선 원칙**: 소스가 이미 B-frame 없는 H.264 + AAC면 어댑터는 `-c copy`로 push한다 (RTSP 어댑터 표준 패턴). 재인코딩은 B-frame 제거 등 계약 준수를 위해 불가피할 때만 허용.
- streamKey는 push URL의 마지막 path segment이며, 이후 모든 산출물(HLS 경로, 상태 API의 `streamKey`/`cameraId`)의 식별자가 된다.

권장 push 커맨드 (재인코딩이 필요한 경우):

```bash
ffmpeg -i <source> \
  -c:v libx264 -profile:v baseline -tune zerolatency -bf 0 -g 30 \
  -c:a aac \
  -f flv rtmp://streaming:1935/live/cam-3f2a9b1c
```

(`-g 30`은 15fps 기준 키프레임 간격 2초. 소스 fps에 맞춰 `2×fps`로 조정한다 — 미지정 시 libx264 기본 keyint 250으로 키프레임 간격 요구를 위반한다.)

(예시의 `cam-3f2a9b1c`는 web-backend 생성 기본값 형식 `cam-{8hex}`를 따른 것 — 형식 자체는 강제되지 않는다.)

### 검증 단언 (TDD)

- **A1-1 (정상 push 지속)**: B-frame 없는 테스트 스트림을 60초 push했을 때 FFmpeg가 조기 종료하지 않으면 OK.
  ```bash
  docker run --rm --network sentinel_sentinel-net linuxserver/ffmpeg \
    -f lavfi -i testsrc=size=640x360:rate=15 -f lavfi -i sine \
    -t 60 -c:v libx264 -profile:v baseline -tune zerolatency -bf 0 -g 30 -c:a aac \
    -f flv rtmp://streaming:1935/live/spec-test-a1
  # exit code 0 → OK / 60초 이전 비정상 종료 → NOK
  ```
- **A1-2 (HLS 자동 생성)**: A1-1 push 시작 후 10초 이내에 `curl -sf http://streaming:8080/live/spec-test-a1/index.m3u8`이 HTTP 200 + `#EXTM3U`로 시작하는 본문을 반환하면 OK.
- **A1-3 (B-frame 거부 — 금지 규칙의 실효성)**: 동일 push를 `-bf 2`로 실행하면 약 5초 내 연결이 끊긴다(FFmpeg broken pipe/EOF 종료). 이 동작이 재현되면 B-frame 금지 조항 유지가 타당함이 확인(OK). 정상 지속된다면 nginx-rtmp 버전이 바뀐 것이므로 스펙 재검토(NOK → 스펙 갱신 트리거).

---

## 계약 2: HLS 라이브 출력 (streaming → 브라우저)

### 입력

- HTTP GET `/live/{streamKey}/index.m3u8` 및 하위 `.ts` 세그먼트 (streaming:8080)
- 브라우저는 이 경로를 직접 알지 못하며, web-backend가 중계한 상대 URL(계약 4)로만 접근한다. 외부 노출 경로: web-frontend nginx가 `/live/` → `streaming:8080` 프록시.

### 출력 (계약)

| 항목 | 값 |
|------|-----|
| Format | HLS (`m3u8` playlist + `ts` 세그먼트, nested 디렉토리) |
| URL pattern | `/live/{streamKey}/index.m3u8` (**상대 경로**) |
| Fragment duration | **max(2초, 소스 키프레임 간격)** — streaming은 키프레임에서만 fragment를 자른다. 소스가 계약 1의 키프레임 간격 요구(≤2초)를 지키면 2초 |
| Playlist length | 10초 (2초 세그먼트 기준 5개) |
| Content-Type | `application/vnd.apple.mpegurl` (m3u8), `video/mp2t` (ts) |
| 응답 헤더 | `Cache-Control: no-cache`, `Access-Control-Allow-Origin: *` |
| Cleanup | 자동 (오래된 `.ts` 자동 제거) |
| Latency | 일반적으로 5~10초 (HLS 특성상 계약 위반 아님) — 소스 키프레임 간격 ≤2초 전제. 간격이 크면 첫 fragment 완결까지 m3u8 생성이 지연되어 초기 지연이 키프레임 간격만큼 늘어난다 |

### 핵심 로직 (동작 — 불변식)

- **무변환 불변식**: HLS 세그먼트의 비디오/오디오 코덱·해상도·프로파일은 RTMP로 push된 원본과 동일하다 (remux only, 트랜스코딩 없음).
- **상대 URL 정책**: 클라이언트에 전달되는 모든 스트림 URL은 `/live/...` 형태의 상대 경로. Docker 내부 주소(`http://streaming:8080/...`) 노출 금지.
- **세그먼트 회전 불변식**: 라이브 중 playlist는 계속 갱신되고(mtime 전진), 오래된 세그먼트는 디스크에서 제거되어 디스크 사용량이 스트림당 상수로 유지된다. (playlist에 나열되는 세그먼트 수 ≠ 디스크상 `.ts` 수 — 클린업은 playlist 창보다 큰 여유를 유지한다. 디스크 보존 상한은 A2-4 참조.)

### 검증 단언 (TDD)

- **A2-1 (playlist 형식/헤더)**: push 중
  ```bash
  curl -si http://streaming:8080/live/{streamKey}/index.m3u8
  ```
  → HTTP 200, `Content-Type: application/vnd.apple.mpegurl`, `Cache-Control: no-cache`, `Access-Control-Allow-Origin: *`, 본문 첫 줄 `#EXTM3U`이면 OK.
- **A2-2 (세그먼트 길이)**: 계약 1의 키프레임 간격 요구(≤2초)를 지키는 push(A1-1 테스트 스트림 등)에서 playlist의 `#EXTINF` 값이 2.0초 ±0.5 범위, 세그먼트 수 ≤ 6이면 OK. (키프레임 간격을 위반하는 소스에는 적용하지 않는다 — fragment 계약이 max(2초, 키프레임 간격)이므로)
- **A2-3 (무변환)**: 원본을 `h264 baseline 640x360`으로 push한 뒤
  ```bash
  ffprobe -v error -show_entries stream=codec_name,profile,width,height \
    http://streaming:8080/live/{streamKey}/index.m3u8
  ```
  → `codec_name=h264`, 해상도·프로파일이 push 원본과 동일하면 OK. 다르면 트랜스코딩이 발생한 것 → NOK.
- **A2-4 (세그먼트 자동 정리)**: 5분 push 후 스트림 디렉토리의 `.ts` 파일 수가 디스크 보존 상한(**≤ 24개**) 이내로 유지되면 OK. 단조 증가하면 NOK.
  - 근거: 디스크 보존량은 playlist 길이(5개)가 아니라 nginx-rtmp `hls_cleanup`의 age 기준으로 결정된다 — 클린업은 age < 2×`hls_playlist_length`(=20초)인 `.ts`만 남긴다. `hls_fragment 2초` 기준 10개이나, fragment는 키프레임에서 잘려 길이가 2초 미만으로 지터되므로 파일 수가 늘어 실측 12~20개다. 진행 중 세그먼트·타이밍 여유를 포함한 상한을 24개로 둔다.
- **A2-5 (재생 지연)**: 키프레임 간격 요구를 지키는 push(A1-1 테스트 스트림 등)에서, push 시작 시점의 실제 콘텐츠가 HLS로 시청 가능해지기까지 15초 이내면 OK.

---

## 계약 3: RTMP 라이브 재배포 (streaming → recording)

### 입력

- RTMP GET(play): `rtmp://streaming:1935/live/{streamKey}` — push 중인 스트림을 내부 서비스가 pull

### 출력 (계약)

- push 중인 원본과 동일한 코덱의 라이브 RTMP 스트림 (무변환 relay)
- push가 없는 streamKey를 pull하면 데이터 없이 대기/실패 (에러 아님 — 소비자가 재시도 책임을 진다)

### 핵심 로직 (동작 — 불변식)

- nginx-rtmp `live` 모드의 표준 동작으로, 동일 application 내 subscriber에게 push 스트림을 그대로 전달한다.
- 현재 유일한 내부 소비자는 recording 서비스 (녹화용 `-c copy` pull). **HLS pull(계약 2)과 달리 브라우저·외부에는 절대 노출하지 않는다** — 내부 서비스 간 전용 경로.
- 소비자가 붙거나 떨어져도 push 측·HLS 출력·상태 판정(계약 4)에 영향이 없어야 한다.

### 검증 단언 (TDD)

- **A3-1 (pull 가능)**: push 중
  ```bash
  ffprobe -v error -show_entries stream=codec_name \
    rtmp://streaming:1935/live/{streamKey}
  ```
  → 스트림 정보(h264 등)가 반환되면 OK.
- **A3-2 (소비자 무간섭)**: RTMP pull 세션을 붙였다 떼어도 같은 streamKey의 HLS playlist 갱신(A2-1)과 `/api/streams`의 `active: true`(A4-2)가 유지되면 OK.

---

## 계약 4: GET /api/streams — 스트림 상태 SSOT

### 입력

- HTTP GET `http://streaming:8080/api/streams` (인증 없음, 내부 네트워크 전용)
- 유일한 정규 소비자: web-backend (브라우저에 URL/상태를 중계). 다른 서비스가 스트림 상태를 자체 판정·보고하는 것은 계약 위반.

### 출력 (계약)

- HTTP 200, `Content-Type: application/json`, 스트림 항목 배열:

```json
[
  {
    "cameraId": "cam-3f2a9b1c",
    "streamKey": "cam-3f2a9b1c",
    "hlsUrl": "/live/cam-3f2a9b1c/index.m3u8",
    "active": true,
    "startedAt": "2026-04-13T09:00:00Z"
  }
]
```

| 필드 | 계약 |
|------|------|
| `cameraId` | RTMP push URL의 `{streamKey}`와 동일 값 |
| `streamKey` | `cameraId`와 항상 동일 |
| `hlsUrl` | 상대 경로, 정확히 `/live/{streamKey}/index.m3u8` |
| `active` | boolean — 아래 판정 기준 |
| `startedAt` | RFC3339 UTC 타임스탬프 |

- 스트림이 하나도 없으면 빈 배열 `[]` (200, null 아님)

### 핵심 로직 (동작 — 불변식)

- **활성 판정 기준 (SSOT)**: 마지막 30초 이내에 해당 스트림의 playlist가 갱신되었으면 `active: true`, 그 외 `false`. (판정 창은 환경변수 `STREAM_ACTIVE_TIMEOUT`(초)으로 조정 가능, 기본 30)
- 판정 근거는 streaming 내부 관측(HLS 산출물 갱신)뿐이다. 어댑터의 자기 보고를 절대 반영하지 않는다.
- web-backend는 이 API 하나만 호출하면 카메라별 시청 URL과 상태를 얻는다 — streaming의 다른 내부 사정을 알 필요가 없다.

### 검증 단언 (TDD)

- **A4-1 (빈 상태)**: 어떤 push도 없는 초기 상태에서 `curl -sf http://streaming:8080/api/streams` → `[]` 이면 OK.
- **A4-2 (활성 반영)**: `spec-test-a1` push 시작 10초 후 응답에 `{"cameraId":"spec-test-a1","streamKey":"spec-test-a1","hlsUrl":"/live/spec-test-a1/index.m3u8","active":true,...}` 항목이 있으면 OK.
  ```bash
  curl -s http://streaming:8080/api/streams | \
    jq -e '.[] | select(.streamKey=="spec-test-a1") | .active == true and .hlsUrl == "/live/spec-test-a1/index.m3u8" and .cameraId == .streamKey'
  ```
- **A4-3 (비활성 전환)**: push 중단 후 40초(판정 창 30초 + 여유) 뒤, 해당 streamKey 항목이 **목록에 없거나**(HLS 산출물 자동 정리로 항목 자체가 소멸할 수 있음), **있으면 `active`가 `false`**면 OK. 항목이 존재하면서 `active: true`면 NOK.
  ```bash
  curl -s http://streaming:8080/api/streams | \
    jq -e '[.[] | select(.streamKey=="spec-test-a1" and .active==true)] | length == 0'
  ```
- **A4-4 (상대 URL 정책)**: 모든 항목의 `hlsUrl`이 정규식 `^/live/[^/]+/index\.m3u8$`에 매치하고 `http`를 포함하지 않으면 OK.
  ```bash
  curl -s http://streaming:8080/api/streams | \
    jq -e 'all(.[]; .hlsUrl | test("^/live/[^/]+/index\\.m3u8$"))'
  ```
- **A4-5 (startedAt 형식)**: 모든 항목의 `startedAt`이 RFC3339 UTC(`...Z`)로 파싱되면 OK.

---

## 계약 5: GET /healthz — 헬스체크

### 입력

- HTTP GET `http://streaming:8080/healthz` (인증 없음)

### 출력 (계약)

- HTTP 200, `Content-Type: application/json`, 본문 `{"status":"ok","service":"streaming"}`

### 핵심 로직 (동작 — 불변식)

- 200 응답은 "HTTP 진입점(8080)과 상태 API 프로세스가 살아 있음"을 의미한다. 개별 스트림의 활성 여부와 무관하다 (스트림 0개여도 healthy).
- Docker healthcheck가 이 엔드포인트를 주기 호출한다 — 응답 지연은 컨테이너 unhealthy 판정으로 이어진다.

### 검증 단언 (TDD)

- **A5-1**: `curl -sf http://streaming:8080/healthz` → HTTP 200, `jq -e '.status=="ok" and .service=="streaming"'` 통과하면 OK.
- **A5-2 (스트림 무관)**: push가 하나도 없는 상태에서도 A5-1이 통과하면 OK.

---

## ⚠️ 리뷰 필요 (문서-코드 불일치)

기존 `docs/interfaces/streaming-api.md`와 실제 코드를 대조하며 발견한 불일치. 스펙 본문에는 코드의 실제 동작 기준으로 반영했으나, 아래 항목은 설계자 확인이 필요하다.

1. **`startedAt`의 의미가 이름과 다름** — 코드는 playlist 파일의 마지막 수정 시각(mtime)을 `startedAt`으로 반환한다 (`services/streaming/main.go`의 `info.ModTime()`). 즉 실제 의미는 "스트림 시작 시각"이 아니라 "마지막 갱신 시각"이며, 라이브 중에는 호출 때마다 값이 전진한다. 필드명 변경(`lastUpdatedAt`) 또는 시작 시각 추적 구현 중 택일 필요.
2. **`/api/streams`는 "활성 스트림 목록"이 아님** — 기존 문서는 응답을 "활성 스트림 목록"이라 기술하지만, 코드는 `/tmp/hls` 아래 playlist가 남아 있는 모든 디렉토리를 반환한다 (`active: false` 항목 포함). push가 끊긴 스트림도 잔존 playlist가 있는 동안 목록에 남는다. "알려진 스트림 전체 + active 플래그"가 실제 계약이며, 본 스펙(계약 4)은 이 기준으로 작성했다.
3. **RTMP pull 경로가 기존 문서에 없음** — 기존 문서는 출력을 "HLS pull"로만 규정하지만, recording 서비스가 `rtmp://streaming:1935/live/{streamKey}`를 직접 pull하여 녹화한다 (`services/recording/main.go`, `STREAMING_RTMP_URL` 환경변수). 실존 접합부이므로 본 스펙에 계약 3으로 승격해 명문화했다 — 이 승격이 설계 의도와 맞는지 확인 필요.
4. **youtube-adapter는 항상 재인코딩** — 기존 문서의 운영 정책은 "가능한 한 copy 모드"이나, youtube-adapter는 무조건 `libx264 300k / aac 48k`로 재인코딩한다 (`services/youtube-adapter/main.go`의 `runFFmpeg`). YouTube 소스의 B-frame 제거를 위한 불가피한 선택으로 보이나(계약 1의 B-frame 금지 준수 목적), "무 트랜스코딩 원칙"의 예외로 인지하고 있어야 한다. cctv-adapter는 문서대로 `-c copy` 준수.
5. **H.264 profile이 push 측에서 pin되지 않음** — 기존 문서는 "Baseline 또는 Main profile 항상 만족"을 계약으로 기술했으나, 실측상 어느 push 측도 `-profile:v`를 명시하지 않는다. youtube-adapter는 `-preset ultrafast`의 부작용(B-frame·CABAC 비활성)으로 사실상 Constrained Baseline을 송출할 뿐이며, cctv-adapter는 `-c copy`라 소스 profile이 그대로 통과한다. 본 스펙(계약 1)은 이를 "profile 강제 없음, Baseline 권장"으로 정정했다. preset 변경 시 profile이 조용히 바뀔 수 있으므로, 계약으로 profile을 보장하려면 push 측 `-profile:v` 명시(pin)가 필요한지 설계자 확인 필요.
6. **키프레임 간격 요구(≤2초)가 push 측에서 일괄 보장되지 않음** — streaming은 키프레임에서만 HLS fragment를 자르므로 계약 2의 fragment 2초·지연 수치는 소스 키프레임 간격 ≤2초를 전제한다. 실측: youtube-adapter는 `-g 60`으로 GOP를 pin하지만 이는 프레임 수 기준이라 소스가 30fps일 때만 2초다(15fps 소스면 4초). cctv-adapter는 `-c copy`라 카메라의 GOP 설정이 그대로 통과하며 어댑터 측 보장이 전혀 없다 — 카메라 설정(키프레임 간격 ≤2초)이 운영 전제조건이 된다. 계약 1에 요구로 명문화했으나(위반 시 push는 수락되고 세그먼트 길이·지연만 늘어남), 어댑터/카메라 설정 지침으로 강제할지 설계자 확인 필요.
