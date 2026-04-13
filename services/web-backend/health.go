package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sort"
	"strconv"
	"sync"
	"time"
)

// ServiceTarget is a monitored container with an HTTP /healthz endpoint.
type ServiceTarget struct {
	Name       string
	HealthzURL string
}

// serviceTargets — web-backend excludes itself (self-check is pointless).
// mosquitto has no HTTP endpoint → deferred to C-2.
var serviceTargets = []ServiceTarget{
	{"hw-gateway", "http://hw-gateway:8080/healthz"},
	{"cctv-adapter", "http://cctv-adapter:8080/healthz"},
	{"youtube-adapter", "http://youtube-adapter:8080/healthz"},
	{"streaming", "http://streaming:8080/healthz"},
	{"recording", "http://recording:8080/healthz"},
	{"notifier", "http://notifier:8080/healthz"},
}

// HealthKind enumerates the two entity kinds we monitor.
const (
	KindService = "service"
	KindSensor  = "sensor"

	StatusHealthy   = "healthy"
	StatusUnhealthy = "unhealthy"
)

// HealthEntry is the per-entity in-memory state used for API responses.
type HealthEntry struct {
	Kind      string    `json:"kind"`
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Status    string    `json:"status"`
	LastCheck time.Time `json:"lastCheck"`
	Since     time.Time `json:"since"`
	Detail    string    `json:"detail"`
	Source    string    `json:"source"`

	// internal — not serialized
	failingSince       time.Time
	consecutiveFailure int
}

// HealthMonitor polls services on a ticker and evaluates sensors.
type HealthMonitor struct {
	db     *sql.DB
	client *http.Client

	mu      sync.RWMutex
	entries map[string]*HealthEntry // keyed by "kind|id"

	stopCh chan struct{}
	wg     sync.WaitGroup
}

func newHealthMonitor(db *sql.DB) *HealthMonitor {
	m := &HealthMonitor{
		db:      db,
		client:  &http.Client{Timeout: 5 * time.Second},
		entries: make(map[string]*HealthEntry),
		stopCh:  make(chan struct{}),
	}
	// seed service entries so /api/health reflects known targets immediately.
	now := time.Now().UTC()
	for _, t := range serviceTargets {
		key := KindService + "|" + t.Name
		m.entries[key] = &HealthEntry{
			Kind:      KindService,
			ID:        t.Name,
			Name:      t.Name,
			Status:    StatusHealthy,
			LastCheck: time.Time{},
			Since:     now,
			Source:    "docker-healthcheck-poll",
		}
	}
	return m
}

// Start launches the monitor goroutine. Cancel ctx or call Stop to terminate.
func (m *HealthMonitor) Start(ctx context.Context) {
	m.wg.Add(1)
	go m.run(ctx)
}

// Stop terminates the monitor goroutine.
func (m *HealthMonitor) Stop() {
	close(m.stopCh)
	m.wg.Wait()
}

func (m *HealthMonitor) run(ctx context.Context) {
	defer m.wg.Done()
	// use a small default tick and re-read interval setting each tick so
	// settings changes take effect within a few seconds.
	minTick := 5 * time.Second
	ticker := time.NewTicker(minTick)
	defer ticker.Stop()

	var lastRun time.Time
	for {
		select {
		case <-ctx.Done():
			return
		case <-m.stopCh:
			return
		case <-ticker.C:
			intervalSec := m.readIntSetting("health.service_check_interval_sec", 30)
			if intervalSec < 5 {
				intervalSec = 5
			}
			if time.Since(lastRun) < time.Duration(intervalSec)*time.Second {
				continue
			}
			lastRun = time.Now()
			m.pollServices()
			m.evaluateSensors()
		}
	}
}

func (m *HealthMonitor) readIntSetting(key string, def int) int {
	v := getSettingValue(m.db, key)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		return def
	}
	return n
}

func (m *HealthMonitor) pollServices() {
	downThresholdSec := m.readIntSetting("health.service_down_threshold_sec", 90)
	now := time.Now().UTC()

	for _, t := range serviceTargets {
		ok, detail := m.probeService(t.HealthzURL)
		key := KindService + "|" + t.Name

		m.mu.Lock()
		e, exists := m.entries[key]
		if !exists {
			e = &HealthEntry{
				Kind:   KindService,
				ID:     t.Name,
				Name:   t.Name,
				Status: StatusHealthy,
				Since:  now,
				Source: "docker-healthcheck-poll",
			}
			m.entries[key] = e
		}
		e.LastCheck = now

		if ok {
			// transition unhealthy → healthy immediately
			e.consecutiveFailure = 0
			e.failingSince = time.Time{}
			if e.Status != StatusHealthy {
				e.Status = StatusHealthy
				e.Since = now
				e.Detail = ""
				m.recordEvent(KindService, t.Name, StatusHealthy, "recovered")
			} else {
				e.Detail = ""
			}
		} else {
			e.consecutiveFailure++
			if e.failingSince.IsZero() {
				e.failingSince = now
			}
			// only flip to unhealthy once the failure has persisted beyond threshold
			if e.Status != StatusUnhealthy && time.Since(e.failingSince) >= time.Duration(downThresholdSec)*time.Second {
				e.Status = StatusUnhealthy
				e.Since = now
				e.Detail = detail
				m.recordEvent(KindService, t.Name, StatusUnhealthy, detail)
			} else if e.Status == StatusUnhealthy {
				e.Detail = detail
			}
		}
		m.mu.Unlock()
	}
}

func (m *HealthMonitor) probeService(url string) (bool, string) {
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return false, fmt.Sprintf("request error: %v", err)
	}
	resp, err := m.client.Do(req)
	if err != nil {
		return false, fmt.Sprintf("http error: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return true, ""
	}
	return false, fmt.Sprintf("HTTP %d", resp.StatusCode)
}

// evaluateSensors walks the devices table and flags stale heartbeats.
func (m *HealthMonitor) evaluateSensors() {
	aliveThresholdSec := m.readIntSetting("health.sensor_alive_threshold_sec", 60)
	now := time.Now().UTC()

	ctx, cancel := dbCtx(context.Background())
	defer cancel()

	rows, err := m.db.QueryContext(ctx, `
		SELECT site_id, device_id, alias, datetime(last_seen)
		FROM devices
		WHERE deleted_at IS NULL
	`)
	if err != nil {
		log.Printf("health: query devices error: %v", err)
		return
	}
	defer rows.Close()

	seen := make(map[string]struct{})

	for rows.Next() {
		var siteID, deviceID, alias, lastSeenStr string
		if err := rows.Scan(&siteID, &deviceID, &alias, &lastSeenStr); err != nil {
			log.Printf("health: scan device error: %v", err)
			continue
		}
		entityID := siteID + ":" + deviceID
		key := KindSensor + "|" + entityID
		seen[key] = struct{}{}

		// SQLite datetime() returns "YYYY-MM-DD HH:MM:SS" in UTC
		lastSeen, err := time.Parse("2006-01-02 15:04:05", lastSeenStr)
		if err != nil {
			continue
		}
		age := now.Sub(lastSeen)
		alive := age <= time.Duration(aliveThresholdSec)*time.Second
		newStatus := StatusHealthy
		detail := ""
		if !alive {
			newStatus = StatusUnhealthy
			detail = fmt.Sprintf("no heartbeat %ds", int(age.Seconds()))
		}

		displayName := alias
		if displayName == "" {
			displayName = deviceID
		}

		m.mu.Lock()
		e, exists := m.entries[key]
		if !exists {
			e = &HealthEntry{
				Kind:   KindSensor,
				ID:     entityID,
				Name:   displayName,
				Status: newStatus,
				Since:  now,
				Source: "mqtt-heartbeat",
			}
			m.entries[key] = e
			// first-seen record the initial status but avoid spurious "first event" —
			// only log unhealthy as an event so timelines stay meaningful.
			if newStatus == StatusUnhealthy {
				m.recordEvent(KindSensor, entityID, StatusUnhealthy, detail)
			}
		} else {
			e.Name = displayName
			e.LastCheck = now
			e.Detail = detail
			if e.Status != newStatus {
				e.Status = newStatus
				e.Since = now
				m.recordEvent(KindSensor, entityID, newStatus, detail)
			}
		}
		m.mu.Unlock()
	}

	// prune sensor entries that are no longer in the devices table (soft-deleted).
	m.mu.Lock()
	for k, e := range m.entries {
		if e.Kind != KindSensor {
			continue
		}
		if _, ok := seen[k]; !ok {
			delete(m.entries, k)
		}
	}
	m.mu.Unlock()
}

// recordEvent inserts a transition row into health_events.
func (m *HealthMonitor) recordEvent(kind, id, status, detail string) {
	ctx, cancel := dbCtx(context.Background())
	defer cancel()
	_, err := m.db.ExecContext(ctx,
		`INSERT INTO health_events (entity_kind, entity_id, status, detail) VALUES (?, ?, ?, ?)`,
		kind, id, status, detail,
	)
	if err != nil {
		log.Printf("health: insert event error: %v", err)
		return
	}
	log.Printf("health event: %s/%s → %s (%s)", kind, id, status, detail)
}

// snapshot returns a sorted copy of the current state for the API.
func (m *HealthMonitor) snapshot() []HealthEntry {
	m.mu.RLock()
	out := make([]HealthEntry, 0, len(m.entries))
	for _, e := range m.entries {
		out = append(out, *e)
	}
	m.mu.RUnlock()

	// unhealthy first; within same status older "since" first (most-stale)
	sort.Slice(out, func(i, j int) bool {
		if out[i].Status != out[j].Status {
			return out[i].Status == StatusUnhealthy
		}
		return out[i].Since.Before(out[j].Since)
	})
	return out
}

// -----------------------------------------------------------------------------
// HTTP handlers
// -----------------------------------------------------------------------------

// handleGetHealth handles GET /api/health — unified services + sensors.
func handleGetHealth(mon *HealthMonitor) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, mon.snapshot())
	}
}

// healthEventResponse is the row shape for /api/health/events.
type healthEventResponse struct {
	ID         int64  `json:"id"`
	EntityKind string `json:"entityKind"`
	EntityID   string `json:"entityId"`
	Status     string `json:"status"`
	DetectedAt string `json:"detectedAt"`
	Detail     string `json:"detail"`
}

// handleListHealthEvents handles GET /api/health/events — recent transitions.
// Query: limit (default 50, max 500), offset (default 0), entity_kind (service|sensor, optional), entity_id (optional).
func handleListHealthEvents(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		limit := 50
		if v := q.Get("limit"); v != "" {
			if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 500 {
				limit = n
			}
		}
		offset := 0
		if v := q.Get("offset"); v != "" {
			if n, err := strconv.Atoi(v); err == nil && n >= 0 {
				offset = n
			}
		}

		entityKind := q.Get("entity_kind")
		entityID := q.Get("entity_id")

		query := `SELECT id, entity_kind, entity_id, status, datetime(detected_at), detail FROM health_events`
		args := []any{}
		whereParts := []string{}
		if entityKind != "" {
			whereParts = append(whereParts, "entity_kind = ?")
			args = append(args, entityKind)
		}
		if entityID != "" {
			whereParts = append(whereParts, "entity_id = ?")
			args = append(args, entityID)
		}
		if len(whereParts) > 0 {
			query += " WHERE " + joinWithAnd(whereParts)
		}
		query += " ORDER BY id DESC LIMIT ? OFFSET ?"
		args = append(args, limit, offset)

		ctx, cancel := dbCtx(r.Context())
		defer cancel()
		rows, err := db.QueryContext(ctx, query, args...)
		if err != nil {
			log.Printf("list health events error: %v", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
			return
		}
		defer rows.Close()

		out := []healthEventResponse{}
		for rows.Next() {
			var ev healthEventResponse
			if err := rows.Scan(&ev.ID, &ev.EntityKind, &ev.EntityID, &ev.Status, &ev.DetectedAt, &ev.Detail); err != nil {
				log.Printf("scan health event error: %v", err)
				continue
			}
			out = append(out, ev)
		}
		writeJSON(w, http.StatusOK, out)
	}
}

func joinWithAnd(parts []string) string {
	s := ""
	for i, p := range parts {
		if i > 0 {
			s += " AND "
		}
		s += p
	}
	return s
}

// Guard for import of json — referenced only via writeJSON upstream; keeping
// this here makes the file self-sufficient if json ever becomes local.
var _ = json.Marshal
