# AGENTS.md — hw-gateway

## Responsibility

The sole S/W contact point with H/W. Communicates with H/W PCs via MQTT and translates signals into HTTP calls to other S/W services.

## Scope

- Subscribe to MQTT crisis signals (`safety/{siteId}/alert`) and forward to notifier + web-backend via HTTP
- Subscribe to MQTT heartbeat signals (`safety/{siteId}/heartbeat`) and maintain equipment status in memory
- Receive restart commands via HTTP from web-backend and publish to MQTT (`safety/{siteId}/cmd/restart`)
- Manage equipment alive/dead status with last heartbeat timestamps (in-memory only)

## Interfaces

### Inbound
| Source | Method | Description |
|--------|--------|-------------|
| H/W PC | MQTT `safety/{siteId}/alert` | Crisis signal received |
| H/W PC | MQTT `safety/{siteId}/heartbeat` | Heartbeat signal |
| web-backend | HTTP POST `/api/restart` | Restart command |

### Outbound
| Target | Method | Description |
|--------|--------|-------------|
| notifier | HTTP POST | Forward crisis event |
| web-backend | HTTP POST | Forward crisis event for WebSocket push |
| H/W PC | MQTT `safety/{siteId}/cmd/restart` | Publish restart command |

### Status API
| Endpoint | Method | Description |
|----------|--------|-------------|
| GET `/api/equipment/status` | HTTP | Return current equipment status (alive/dead, last heartbeat) |

## Implementation Notes

- MQTT client must auto-reconnect on broker disconnect
- Heartbeat timeout threshold should be configurable (e.g., 30s default)
- Equipment status is in-memory only — no DB persistence needed
- Crisis signal forwarding to notifier and web-backend should be parallel (non-blocking)
- All HTTP calls to other services should have reasonable timeouts and error logging
