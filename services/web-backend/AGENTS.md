# AGENTS.md — web-backend

## Responsibility

Central REST API + WebSocket server. Manages all data, authentication, temporary links, and coordinates between services and clients.

## Scope

- REST API for all CRUD operations (contacts, cameras, sites, incidents, accounts)
- WebSocket server for real-time client notifications (crisis alerts)
- SQLite database management (volume-mounted)
- Authentication: login, sign-up, admin approval flow
- Temporary link generation/validation/revocation (JWT, 24h expiry)
- Forward restart commands to hw-gateway
- Provide camera/HLS URL list (fetched from streaming service)

## Interfaces

### Inbound
| Source | Method | Description |
|--------|--------|-------------|
| web-frontend | REST + WebSocket | All client interactions |
| hw-gateway | HTTP POST `/api/incidents` | Crisis event notification |
| notifier | HTTP GET `/api/contacts` | Fetch notification targets |
| notifier | HTTP POST `/api/links/temp` | Request temporary link |
| notifier | HTTP POST `/api/alarms` | System alarm on total notify failure |

### Outbound
| Target | Method | Description |
|--------|--------|-------------|
| hw-gateway | HTTP POST | Restart command |
| streaming | HTTP GET | Fetch HLS URL list |
| web-frontend (clients) | WebSocket | Push crisis alerts |

### REST API Groups
| Group | Endpoints |
|-------|-----------|
| Auth | POST /auth/login, POST /auth/register, POST /auth/approve |
| Contacts | GET/POST/PUT/DELETE /api/contacts |
| Sites | GET/PUT /api/sites |
| Cameras | GET /api/cameras |
| Incidents | GET /api/incidents |
| Links | POST /api/links/temp, GET /api/links/verify/{token}, DELETE /api/links/{id} |
| Equipment | GET /api/equipment/status, POST /api/equipment/restart |
| Alarms | POST /api/alarms, GET /api/alarms |

## Data

- SQLite with volume mount at `/data/sentinel.db`
- Tables: contacts, cameras, sites, incidents (see root AGENTS.md for schema)
- In-memory: JWT blacklist for revoked temporary links

## Authentication

| Role | Access | Method |
|------|--------|--------|
| Admin | Full | JWT login |
| User | View + granted | JWT login (admin-approved) |
| Temp Viewer | CCTV only | JWT temp link (24h) |

- Built-in admin account on first run
- Sign-up requires admin approval

## Implementation Notes

- SQLite is sufficient — no need for a separate DB container
- WebSocket connections should be lightweight (push only, no polling)
- Temporary links use JWT — no DB storage needed, only blacklist in memory
- Crisis event from hw-gateway should immediately trigger WebSocket broadcast
- All service-to-service HTTP calls need timeout + error handling
