# AGENTS.md — notifier

## Responsibility

Crisis propagation service. Sends KakaoTalk/SMS notifications on crisis events with fallback logic.

## Scope

- Receive crisis events from hw-gateway
- Request temporary CCTV link from web-backend and include in message
- Fetch notification target list from web-backend API
- Send KakaoTalk notification -> on failure, SMS retry -> on failure, web system alarm
- Log all notification attempts and results

## Interfaces

### Inbound
| Source | Method | Description |
|--------|--------|-------------|
| hw-gateway | HTTP POST `/api/notify` | Crisis event to propagate |

### Outbound
| Target | Method | Description |
|--------|--------|-------------|
| web-backend | HTTP GET `/api/contacts` | Fetch notification targets |
| web-backend | HTTP POST `/api/links/temp` | Request temporary CCTV link |
| web-backend | HTTP POST `/api/alarms` | System alarm (when all channels fail) |
| KakaoTalk API | External API | Send KakaoTalk message |
| SMS API | External API | Send SMS message (fallback) |

## Fallback Chain

```
1. KakaoTalk API -> success -> done
2. KakaoTalk fails -> SMS API -> success -> done
3. SMS fails -> web-backend system alarm -> done (degraded)
```

## Implementation Notes

- Fallback logic must be robust — never silently fail
- Each notification attempt (success/failure) should be logged
- External API credentials should be loaded from environment variables
- Message template should include: crisis description, site info, temporary CCTV link
- Notification to multiple contacts should be parallel
- Timeout for external APIs should be reasonable (5-10s)

## Architecture Decisions

- POST /api/notify returns 200 immediately (async dispatch via goroutine) — hw-gateway does not wait for delivery
- KakaoTalk 알림톡 API uses X-Api-Key and X-Sender-Key headers for authentication
- If KAKAO_API_URL or KAKAO_API_KEY env vars are empty, KakaoTalk sending is skipped (logged as not configured)
- Temp link failure triggers degraded mode: notification sent without CCTV link
- Contact fetch failure aborts the entire notification for that event (no contacts = nothing to send)
- Config env vars: WEB_BACKEND_URL, KAKAO_API_URL, KAKAO_API_KEY, KAKAO_SENDER_KEY, KAKAO_TEMPLATE_CODE
