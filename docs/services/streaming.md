# streaming

> **Reader scope:** 이 서비스를 구현·수정하는 Claude 세션 전용.
> 다른 서비스의 내부 구현을 읽지 마세요. 외부와의 계약은 아래 "Interfaces" 섹션의 링크만 참조.
> 시스템 전체 그림은 orchestrator 세션 영역(`docs/architecture-overview.md`)이며 본 세션은 읽을 필요 없음.

## Responsibility

중앙 HLS 스트리밍 허브. 모든 adapter가 RTMP로 push하면 **remux만** 수행해 HLS(m3u8+ts)로 서빙한다. **트랜스코딩 없음** — CPU를 최소로 유지한다. 스트림 alive/dead의 유일한 권위 소스.

## Interfaces

| Boundary | Direction | Spec |
|----------|-----------|------|
| RTMP (adapters → 본 서비스) | inbound | [../interfaces/streaming-api.md](../interfaces/streaming-api.md) — "RTMP Input Specification" |
| HLS (clients, web-frontend direct) | outbound | [../interfaces/streaming-api.md](../interfaces/streaming-api.md) — "HLS Output Specification" |
| HTTP `/api/streams` (web-backend 조회) | inbound | 본 문서 "HTTP API" |

## Code Structure

**하이브리드 컨테이너, 두 프로세스**:

1. **nginx-rtmp** — `services/streaming/nginx.conf`
   - 1935/TCP: RTMP 수신
   - 8080/TCP: HTTP (HLS 파일 + `/api/*`, `/healthz`를 Go로 proxy)
   - `hls_nested on` → `/tmp/hls/{streamKey}/index.m3u8`
   - `hls_cleanup on` → 오래된 .ts 자동 삭제
2. **Go streaming-api** — `services/streaming/main.go` (~85 lines)
   - 8081/TCP: `/healthz`, `/api/streams` (내부)
   - `/tmp/hls/` 스캔하여 playlist mtime < 30s면 "active"

`entrypoint.sh`: Go를 백그라운드로, nginx를 foreground로 실행.

## Environment Variables

compose에서 별도 env 없음 (기본 포트/경로 하드코딩).

## Build & Run

```bash
docker compose build streaming
docker compose up -d streaming
docker compose logs -f streaming
```

- 포트: 내부만. 1935(RTMP), 8080(HTTP) — 외부 노출 없음. web-frontend nginx가 `/live/`를 이 서비스로 proxy.
- 헬스: `GET /healthz` (compose가 체크)
- 단독 테스트: adapter 컨테이너에서 `ffmpeg -re -i sample.mp4 -c:v libx264 -bf 0 -c:a aac -f flv rtmp://streaming:1935/live/test` → `curl http://streaming:8080/live/test/index.m3u8`

## HTTP API

| Method | Path | Response |
|--------|------|----------|
| GET | `/healthz` | `200` |
| GET | `/api/streams` | `[{cameraId, hlsUrl, active, startedAt}]` — active는 playlist mtime < 30s |
| GET | `/live/{streamKey}/index.m3u8` | HLS playlist |
| GET | `/live/{streamKey}/*.ts` | HLS segment |

`hlsUrl`은 **상대 경로** (`/live/{streamKey}/index.m3u8`). 절대 URL 조립은 호출자 몫.

## Constraints / Known Issues

- **B-frame 입력은 절대 금지**. nginx-rtmp v1.2.2가 ~5초 후 audio packet muxing 에러로 connection reset. 증상이 오디오 쪽으로 보이지만 원인은 video B-frame.
- 새 adapter 제작 시 [streaming-api.md](../interfaces/streaming-api.md)와 [adapter-checklist.md](../adapter-checklist.md)를 반드시 따를 것.
- Fragment 2초, playlist 10초(5 segments). 끊김 없는 live 지연 ≈ 4~6초.
- `/tmp/hls/`는 tmpfs(컨테이너 rootfs). 재시작 시 초기화 (의도된 동작).

## Storage / State

- 영구 저장 없음. HLS 세그먼트는 `/tmp/hls/` (휘발). 녹화는 recording 서비스가 별도로 RTMP를 구독하여 수행.
