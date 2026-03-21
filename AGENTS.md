# AGENTS.md — Sentinel (Root)

Industrial safety real-time monitoring system for hazardous workplaces.

## Project Overview

Real-time crisis detection and notification system for preventing workplace injuries.

**Core capabilities:**
- Receive crisis signals from H/W (scream/help detection, auto equipment stop)
- Send KakaoTalk/SMS alerts with temporary CCTV links on crisis
- Continuous CCTV monitoring (multi-camera, switchable)
- Remote equipment restart via web UI
- 119 emergency call with location info
- Mobile-first web interface

## Design Principles

1. **H/W-S/W layer separation** — S/W only receives signals. No H/W internals.
2. **Single H/W contact point** — hw-gateway is the only H/W interface.
3. **External abstraction** — cctv-adapter abstracts camera types.
4. **No transcoding** — Mini PC resource preservation. Cameras must output H.264.
5. **Mobile first** — All UI designed for mobile screens.
6. **Lightweight first** — Runs on a single on-premise mini PC. Minimize processes.
7. **Ralph Loop development** — API design first, then parallel front/back implementation.

## Architecture

### Layer Structure

```
[ H/W Layer ]
  H/W PC (sends/receives via MQTT)
  CCTV Cameras (H.264, multiple)

[ S/W Layer ]
  hw-gateway    <-MQTT->  H/W PC
  cctv-adapter  <-RTSP->  CCTV Cameras
  notifier
  streaming
  web-backend
  web-frontend
```

### Communication

| Path | Method |
|------|--------|
| H/W <-> hw-gateway | MQTT |
| Inter-service (S/W) | HTTP |
| web-backend <-> client | REST + WebSocket |
| CCTV -> client | HLS (web-backend only passes URLs) |

### Crisis Flow

```
H/W -> hw-gateway -> (HTTP) -> notifier
                                 -> Request temp link from web-backend
                                 -> Send KakaoTalk (fallback: SMS, then web alarm)
                  -> (HTTP) -> web-backend -> (WebSocket) -> client notification
```

### Restart Flow

```
Client -> web-backend (REST) -> hw-gateway (HTTP) -> H/W (MQTT)
```

## Container Configuration (Docker)

| Container | Role | External |
|-----------|------|----------|
| hw-gateway | H/W gateway. Built-in MQTT client | Internal |
| cctv-adapter | Camera type abstraction. Pass-through | Internal |
| streaming | HLS serving. No transcoding | Internal |
| notifier | KakaoTalk/SMS dispatch. Fallback logic | Internal |
| web-backend | REST API + WebSocket. SQLite volume mount | Internal |
| web-frontend | Mobile-first UI | External (80/443) |

- Deployment: SSH + docker compose
- OS: Linux
- Hardware: Single on-premise mini PC

## Data Model (SQLite)

### contacts (notification targets)
| Field | Type |
|-------|------|
| id | PK |
| name | TEXT |
| phone | TEXT |

### cameras
| Field | Type |
|-------|------|
| id | PK |
| name | TEXT |
| location | TEXT (site address) |
| zone | TEXT (zone text, e.g., "Factory 1 press area") |

### sites
| Field | Type |
|-------|------|
| id | PK |
| address | TEXT |
| manager_name | TEXT |
| manager_phone | TEXT |

### incidents
| Field | Type |
|-------|------|
| id | PK |
| occurred_at | DATETIME |
| confirmed_at | DATETIME |
| confirmed_by | TEXT |

### In-Memory (not in DB)
- Equipment status: alive flag + last heartbeat time
- Temporary links: JWT self-contained (only blacklist in memory)

## Authentication & Authorization

| Role | Access | Permissions |
|------|--------|-------------|
| Admin | Login | Full access + account approval + link create/revoke |
| User | Login (admin-approved) | View + admin-granted features |
| Temp Viewer | JWT link (24h) | CCTV view only |

- Built-in initial admin account, not externally exposed
- Sign-up -> admin approval -> activation

## Failure Handling

| Failure | Response |
|---------|----------|
| KakaoTalk failure | SMS retry -> both fail: web system alarm |
| Heartbeat timeout | Warning on web UI |
| Camera stream lost | "Disconnected" on that channel |

## Ralph Loop Workflow

### Development Order
1. API design session — Generate API spec docs (OpenAPI or markdown)
2. MQTT topic design — Generate hw-gateway interface docs
3. Parallel service implementation — API spec as contract
4. Integration verification — E2E flow tests

### File Memory Structure
- `prd.json`: All user stories + completion status
- `progress.txt`: Learnings from previous iterations
- `AGENTS.md` (root): Project-wide patterns/rules
- `services/{service}/AGENTS.md`: Per-service responsibility/scope/rules

### Story Size Principle
- One story = completable in one context window
- If it can't be explained in 2-3 sentences, split it
- Acceptance criteria must be verifiable

## MQTT Topics

| Topic | Direction | Purpose |
|-------|-----------|---------|
| `safety/{siteId}/alert` | Subscribe | Crisis signal |
| `safety/{siteId}/heartbeat` | Subscribe | Alive signal |
| `safety/{siteId}/cmd/restart` | Publish | Restart command |
