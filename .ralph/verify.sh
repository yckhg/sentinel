#!/bin/bash
set -e
PROJECT_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
ERRORS=0
pass() { echo "  ✓ $1"; }
fail() { echo "  ✗ $1"; ERRORS=$((ERRORS + 1)); }

echo "=== 1. Docker Compose Config ==="
if docker compose -f "$PROJECT_ROOT/docker-compose.yml" config -q 2>/dev/null; then
  pass "docker-compose.yml valid"
else
  fail "docker-compose.yml invalid"
fi

echo "=== 2. Go Vet (backend services) ==="
for svc in hw-gateway cctv-adapter streaming notifier web-backend youtube-adapter; do
  SVC_DIR="$PROJECT_ROOT/services/$svc"
  if [ -f "$SVC_DIR/go.mod" ]; then
    if docker compose -f "$PROJECT_ROOT/docker-compose.yml" exec -T "$svc" go vet ./... 2>/dev/null; then
      pass "$svc: go vet"
    else
      fail "$svc: go vet"
    fi
  fi
done

echo "=== 3. Frontend Typecheck ==="
SVC_DIR="$PROJECT_ROOT/services/web-frontend"
if [ -f "$SVC_DIR/tsconfig.json" ]; then
  if docker compose -f "$PROJECT_ROOT/docker-compose.yml" exec -T web-frontend npx tsc --noEmit 2>/dev/null; then
    pass "web-frontend: typecheck"
  else
    fail "web-frontend: typecheck"
  fi
fi

echo ""
echo "==============================="
if [ "$ERRORS" -eq 0 ]; then
  echo "결과: 전체 통과"; exit 0
else
  echo "결과: ${ERRORS}개 실패"; exit 1
fi
