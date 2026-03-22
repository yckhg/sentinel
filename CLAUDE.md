# CLAUDE.md — Sentinel

Industrial safety real-time monitoring system for hazardous workplaces.

## Stack

- 6 Docker containers on a single mini PC (on-premise)
- SQLite for persistence (volume-mounted)
- MQTT for H/W communication
- HLS for video streaming (no transcoding)
- Mobile-first web UI

## Quick Commands

```bash
docker compose up -d          # Start all services
docker compose down            # Stop all services
docker compose logs -f         # Follow logs
docker compose logs -f <svc>   # Follow specific service logs
```

## Workflow & Task Division
@~/.claude/docs/ralph-workflow.md

Key files:
- `AGENTS.md` — Architecture and design principles
- `.ralph/prd.json` — User stories and completion tracking
- `.ralph/progress.txt` — Iteration learnings
- `services/{name}/AGENTS.md` — Per-service documentation

## Architecture

6 services: hw-gateway, cctv-adapter, streaming, notifier, web-backend, web-frontend.
See `AGENTS.md` for full architecture details.
