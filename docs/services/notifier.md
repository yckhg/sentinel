# notifier

> **Reader scope:** 이 서비스를 구현·수정하는 Claude 세션 전용.
> 다른 서비스의 내부 구현을 읽지 마세요. 외부와의 계약은 아래 "Interfaces" 섹션의 링크만 참조.
> 시스템 전체 그림은 orchestrator 세션 영역(`docs/architecture-overview.md`)이며 본 세션은 읽을 필요 없음.

## Responsibility

Crisis 전파 서비스. hw-gateway에서 crisis 이벤트를 받아 각 contact에게 KakaoTalk → SMS → web 시스템 알람 순서로 fallback 전송한다. 각 contact는 독립적으로 fallback 체인을 타며, 한 채널 성공 시 그 contact는 종료.

## Interfaces

| Boundary | Direction | Spec |
|----------|-----------|------|
| hw-gateway (`POST /api/notify`) | inbound | 본 문서 "HTTP API" |
| web-backend | outbound (REST) | 본 문서 "Outbound Calls" |
| KakaoTalk 알림톡 API | outbound (external) | X-Api-Key + X-Sender-Key |
| NHN Cloud SMS v3.0 | outbound (external) | X-Secret-Key |

## Code Structure

- `services/notifier/main.go` 단일 파일.
- `POST /api/notify`는 즉시 200 반환 후 goroutine으로 dispatch (hw-gateway는 기다리지 않음).
- 각 contact 병렬 goroutine. 내부는 순차 fallback.
- External API 타임아웃 5~10s.

## Environment Variables

| Var | Meaning |
|-----|---------|
| `WEB_BACKEND_URL` | contacts/links/alarms 호출 대상 |
| `FRONTEND_URL` | temp link URL 조립: `{FRONTEND_URL}/view/{token}` |
| `RECORDING_URL` | (필요 시 녹화 참조 — 현재 메시지 포함 여부 구현 확인) |
| `KAKAO_API_URL`, `KAKAO_API_KEY`, `KAKAO_SENDER_KEY`, `KAKAO_TEMPLATE_CODE` | 빈 값이면 KakaoTalk 스킵 |
| `NHN_SMS_APP_KEY`, `NHN_SMS_SECRET_KEY`, `NHN_SMS_SENDER_NO` | 빈 값이면 SMS 스킵 |
| `SMTP_HOST/PORT/USER/PASS/FROM` | (확장 이메일 경로, 선택) |

## Build & Run

```bash
docker compose build notifier
docker compose up -d notifier
docker compose logs -f notifier
```

- 포트: 내부 8080 (헬스/`/api/notify`)
- 헬스: `GET /healthz`
- 단독 테스트: `curl -X POST http://sentinel-notifier:8080/api/notify -d '{...crisis...}'` (다른 컨테이너에서)

## HTTP API

| Method | Path | Body | Response |
|--------|------|------|----------|
| POST | `/api/notify` | crisis event `{siteId, deviceId, description, timestamp}` | `200 OK` 즉시 (async) |
| POST | `/api/send-email` | 이메일 직접 발송 (제목/본문/수신자) | 발송 결과 |
| GET | `/healthz` | — | `200` |

## Outbound Calls

- **web-backend** `GET /api/contacts` — 전송 대상 목록. 실패 시 해당 이벤트 전체 abort (no silent fail이지만 대상이 없으면 보낼 수 없음).
- **web-backend** `POST /api/links/temp` — temp CCTV link 요청. 실패 시 degraded mode (링크 없이 전송).
- **web-backend** `POST /api/alarms` — 모든 외부 채널 실패 시 시스템 알람. KakaoTalk/SMS 둘 다 미설정이어도 호출 (silent fail 금지).
- **KakaoTalk** — 1순위. 알림톡 템플릿 기반.
- **NHN SMS** — 2순위. `https://api-sms.cloud.toast.com/sms/v3.0/appKeys/{appKey}/sender/sms`.

받는 쪽 내부 구현은 알 필요 없음.

## Fallback Chain (per-contact)

```
KakaoTalk 성공 → done
  ↓ 실패/미설정
SMS 성공 → done
  ↓ 실패/미설정
web-backend /api/alarms (항상 호출, degraded done)
```

## Constraints / Known Issues

- 실패는 반드시 로그로 남긴다 (silent fail 금지).
- temp link 실패해도 알림은 보낸다 (링크 없는 메시지).
- contact fetch 실패는 이벤트 전체 중단 (보낼 대상이 없음).
- KakaoTalk 알림톡 템플릿은 사전 등록 필수.

## Storage / State

- 영구 저장 없음. 전송 이력은 로그에만 남음 (웹에서 감사 기능 필요하면 web-backend 쪽 alarms 테이블로 기록 경로 있음).
