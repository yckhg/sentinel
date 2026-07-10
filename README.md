# safety-monitor (Sentinel)

**위험 작업장을 위한 산업안전 실시간 영상·센서 관제 시스템**

CCTV/유튜브 라이브 영상과 현장 센서(MQTT)를 한곳에서 실시간으로 지켜보고, 위기 상황을 감지하면 즉시 알림(SMS·카카오·웹)을 보내는 온프레미스 시스템입니다. 미니 PC 한 대(on-premise) 위에서 Docker로 동작합니다.

> `safety-*` 제품군 중 **관제(monitor)** 컴포넌트입니다. 영상 감시 + 센서 관제 + 위기 알림을 담당합니다.

---

## 무엇을 하는가

- **영상 수집·배포** — CCTV(RTSP)와 유튜브 라이브를 받아 HLS로 변환, 모바일 웹에서 재생 (트랜스코딩 없음)
- **하드웨어 연동** — ESP32 음성기기 등 현장 장비와 MQTT로 통신, 하트비트로 장비 생존 감시
- **위기 감지·알림** — 센서/버튼 이벤트를 받아 SMS·카카오 알림톡·웹 푸시로 라우팅
- **녹화·보존** — 라이브 세그먼트를 롤링 저장하고 아카이브
- **모바일 우선 웹 UI** — 현장/관리자가 폰으로 즉시 확인

## 아키텍처

Docker Compose 위 9개 컨테이너. 모든 서비스는 내부 네트워크(`sentinel-net`)로 통신하고, 외부에는 **웹 UI(3080)** 와 **MQTT(20011)** 만 노출합니다.

| 서비스 | 역할 | 외부 포트 |
|---|---|---|
| **mosquitto** | MQTT 브로커 (H/W·ESP32 통신) | `20011:1883` |
| **hw-gateway** | MQTT ↔ DB ↔ notifier 중계, 장비 하트비트 감시 | 내부 전용 |
| **cctv-adapter** | RTSP CCTV → RTMP/HLS 변환 | 내부 전용 |
| **youtube-adapter** | YouTube Live → RTMP/HLS 변환 | 내부 전용 |
| **streaming** | HLS 세그먼트 배포 | 내부 전용 |
| **recording** | 세그먼트 롤링 저장·아카이브 | 내부 전용 |
| **notifier** | 알림 라우팅 (SMS/Kakao/Web) | 내부 전용 |
| **web-backend** | HTTP API + WebSocket 허브 (SQLite) | 내부 전용 |
| **web-frontend** | 모바일 우선 React UI (nginx) | `3080:80` |

**스택:** Docker · SQLite(볼륨 영속) · MQTT · HLS(무트랜스코딩) · 모바일 우선 웹

## 빠른 시작

```bash
docker compose up -d          # 전체 서비스 기동
docker compose logs -f         # 로그 팔로우
docker compose logs -f <svc>   # 특정 서비스 로그
docker compose down            # 전체 정지
```

기동 후 웹 UI: **http://localhost:3080**

### 설정 파일

| 파일 | 용도 |
|---|---|
| `config/cameras.json` | CCTV 카메라 목록 (RTSP URL 등) |
| `config/youtube-sources.json` | 유튜브 라이브 소스 목록 |
| `.env` | 알림 자격증명(Kakao/NHN SMS/SMTP), 관리자 계정 등 |

### E2E 테스트

```bash
docker compose --profile test run --rm e2e-crisis    # 위기 알림 시나리오
docker compose --profile test run --rm e2e-restart   # 재기동 복원 시나리오
```

## 문서

전체 문서는 [`docs/`](docs/)에 있으며, **MSA-스타일 컨텍스트 격리** 원칙으로 역할별 진입점이 나뉩니다. 먼저 [`docs/README.md`](docs/README.md)(역할별 문서 맵)를 읽고 필요한 최소 문서만 로드하세요.

- **아키텍처 전체 그림** → [`docs/architecture-overview.md`](docs/architecture-overview.md)
- **서비스 간 계약(SSOT)** → [`docs/interfaces/`](docs/interfaces/) — MQTT / streaming / web-api
- **서비스별 구현 가이드** → [`docs/services/`](docs/services/)
- **계약 스펙·검증** → [`docs/spec/`](docs/spec/)
- **펌웨어(H/W) 개발자** → [`docs/interfaces/mqtt-publisher-guide.md`](docs/interfaces/mqtt-publisher-guide.md) 단일 진입점
