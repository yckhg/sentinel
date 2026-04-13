# youtube-adapter

> **Reader scope:** 이 서비스를 구현·수정하는 Claude 세션 전용.
> 다른 서비스의 내부 구현을 읽지 마세요. 외부와의 계약은 아래 "Interfaces" 섹션의 링크만 참조.
> 시스템 전체 그림은 orchestrator 세션 영역(`docs/architecture-overview.md`)이며 본 세션은 읽을 필요 없음.

## Responsibility

YouTube URL 또는 로컬 비디오 파일을 소스로 삼아 streaming 서버의 RTMP 입력 규격에 맞춰 push하는 demo/test adapter. cctv-adapter가 참조 구현이며, 본 서비스는 **소스 특성상 re-encode가 필수**라는 점이 가장 큰 차이.

## Interfaces

| Boundary | Direction | Spec |
|----------|-----------|------|
| 로컬 파일(`/media/*.mp4`, read-only mount) | inbound (file) | — |
| YouTube (yt-dlp) | inbound (URL) | youtube.com/watch, youtu.be만 허용 |
| streaming 서버 (RTMP push) | outbound | [../interfaces/streaming-api.md](../interfaces/streaming-api.md) — "RTMP Input Specification" |
| web-backend (reload trigger 가능) | inbound (필요 시) | cctv-adapter와 동일 패턴 |

## Code Structure

단일 파일: `services/youtube-adapter/main.go`. 소스별 goroutine → FFmpeg 프로세스. 로컬 파일 우선, 없으면 yt-dlp로 resolve.

## Environment Variables

| Var | Default | Meaning |
|-----|---------|---------|
| `YOUTUBE_CONFIG_PATH` | `/config/youtube-sources.json` | 소스 목록 |
| `STREAMING_RTMP_URL` | `rtmp://streaming:1935/live` | RTMP 베이스 |
| `WEB_BACKEND_URL` | `http://web-backend:8080` | reload fetch 대상 |

Config 예시:
```json
[{"id": "yt-cam-1", "youtubeUrl": "https://youtu.be/...", "streamKey": "yt-cam-1", "localFile": "/media/yt-cam-1.mp4"}]
```
- `localFile` 있으면 파일 재생(권장, 안정). 비면 yt-dlp.

## Build & Run

```bash
docker compose build youtube-adapter
docker compose up -d youtube-adapter
docker compose logs -f youtube-adapter
```

- 포트: 내부 8080 (헬스)
- 헬스: `GET /healthz`
- Docker 이미지: FFmpeg + yt-dlp 포함
- 리소스: cpus 2.0 / mem 512M (re-encode 때문에 cctv-adapter보다 큼)
- 볼륨: `./media:/media:ro`

## FFmpeg Invocation

YouTube 비디오는 거의 항상 B-frame 포함 → `-c copy` **금지**. 재인코딩 필수:

```
ffmpeg -re -stream_loop -1 -i <file-or-yt-dlp-stdout> \
  -c:v libx264 -preset ultrafast -tune zerolatency -b:v 300k -g 60 \
  -c:a aac -b:a 48k \
  -f flv rtmp://streaming:1935/live/{streamKey}
```

- `-re` : 로컬 파일 native frame rate 재생 (실시간 흉내)
- `-stream_loop -1` : 로컬 파일 무한 반복 (최근 수정 반영; US-002)
- `-tune zerolatency -bf 0` : B-frame 제거 (streaming 입력 규격 준수)
- yt-dlp는 URL을 best format으로 resolve → FFmpeg stdin 혹은 URL pass

## HTTP API

| Method | Path | Purpose |
|--------|------|---------|
| GET | `/healthz` | 헬스 |
| GET | `/api/streams/status` | 각 소스별 FFmpeg/yt-dlp 상태 |
| POST | `/api/cameras/reload` | (필요 시) 소스 목록 재조회 |

## Outbound Calls

- **RTMP push** → `rtmp://streaming:1935/live/{streamKey}` (H.264 no B-frame + AAC FLV).
- **web-backend** — reload 시 소스 목록 fetch (구현되어 있다면).

## Constraints / Known Issues

- **yt-dlp는 IP당 동시 스트림 1개 권장**. 여러 YouTube URL을 동시에 돌리면 rate-limit/IP block 위험. 데모/단일 스트림 용도로 제한.
- YouTube URL 검증: `youtube.com/watch` 또는 `youtu.be`, 최대 200자, yt-dlp 타임아웃 30s.
- 로컬 파일 경로는 `/media/` 하위로 제한 (compose read-only mount).
- 재시작 backoff: 1s → 30s, clean exit 시 reset.
- 0바이트 세그먼트 등 streaming 쪽 부작용은 streaming 세션의 책임, 본 세션은 RTMP 송출 스펙 준수만 보장.
- B-frame 제거를 게을리하면 ~5초 후 streaming 쪽에서 connection reset (증상이 audio packet 쪽으로 보이지만 원인은 video).

## Storage / State

- 영구 저장 없음. config 파일 read-only. in-memory로 FFmpeg 핸들/상태만 관리.
