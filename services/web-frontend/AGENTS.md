# AGENTS.md — web-frontend

## Responsibility

Mobile-first web UI for monitoring, management, and emergency response.

## Scope

- Bottom tab bar navigation: CCTV / Incidents / Management / Settings
- CCTV multi-view with camera switching (main screen)
- Real-time crisis alert banner via WebSocket
- Management tab: contacts CRUD, site info, temp link management, account management
- Viewer-only page at `/view/{token}` (CCTV viewing only, no auth)
- 119 emergency call button with location info
- Equipment restart button with 2-step confirmation
- Incident history page

## Pages & Navigation

### Bottom Tab Bar
| Tab | Content |
|-----|---------|
| CCTV | Multi-camera view + camera switching |
| Incidents | Crisis history list |
| Management | Contacts, sites, links, accounts |
| Settings | User settings |

### Special Pages
| Route | Description | Auth |
|-------|-------------|------|
| `/login` | Login page | None |
| `/register` | Sign-up page | None |
| `/view/{token}` | Viewer-only CCTV page | JWT temp link |

## Interfaces

### Inbound
| Source | Method | Description |
|--------|--------|-------------|
| web-backend | WebSocket | Crisis alert push |
| streaming | HLS | Video streams (direct from streaming server) |

### Outbound
| Target | Method | Description |
|--------|--------|-------------|
| web-backend | REST | All API calls |

## Key UI Behaviors

- **Crisis alert**: Full-width banner at top, persistent until dismissed. Includes incident details and quick actions.
- **CCTV view**: HLS.js player. Grid layout for multi-camera. Tap to enlarge single camera.
- **119 button**: Uses browser Geolocation API. Shows confirmation before dialing.
- **Restart button**: 2-step confirmation dialog (Are you sure? -> Final confirm with reason input).
- **Viewer page**: Minimal UI — only CCTV player, no navigation or management features.

## Implementation Notes

- Mobile-first design — desktop is secondary
- HLS playback via HLS.js (or native on Safari)
- WebSocket reconnect on disconnect with exponential backoff
- Camera stream URLs come from web-backend API (not hardcoded)
- Viewer page must work without login — JWT token in URL is the auth
- Keep bundle size small — mini PC serves this
