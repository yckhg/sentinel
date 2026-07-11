package main

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"sync/atomic"
	"syscall"
	"time"

	_ "modernc.org/sqlite"
)

func main() {
	dbPath := os.Getenv("DB_PATH")
	if dbPath == "" {
		dbPath = "/data/sentinel.db"
	}

	db, err := initDB(dbPath)
	if err != nil {
		log.Fatalf("failed to initialize database: %v", err)
	}
	defer db.Close()

	if err := runMigrations(db); err != nil {
		log.Fatalf("failed to run migrations: %v", err)
	}

	initJWTSecret()
	initHWGatewayURL()
	initServiceURLs()
	initNotifierURL()
	initTrustedProxies()

	if err := seedAdminUser(db); err != nil {
		log.Fatalf("failed to seed admin user: %v", err)
	}

	// Start unified health monitor (services + sensors).
	healthMonitor := newHealthMonitor(db)
	monitorCtx, monitorCancel := context.WithCancel(context.Background())
	defer monitorCancel()
	healthMonitor.Start(monitorCtx)

	// Periodic + size-triggered TRUNCATE checkpoint keeps the -wal file bounded
	// under load and reclaims it once writes quiesce (WAL hygiene contract).
	startWALCheckpoint(db, dbPath)

	mux := http.NewServeMux()

	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"ok","service":"web-backend"}`))
	})

	// Rate limiters for public auth endpoints
	loginLimiter := newRateLimiter(10, time.Minute)    // 10 req/min per IP
	registerLimiter := newRateLimiter(5, time.Minute)  // 5 req/min per IP
	startRateLimitCleanup(loginLimiter, registerLimiter)

	// Auth routes (public — rate limited)
	mux.HandleFunc("POST /auth/register", rateLimitMiddleware(registerLimiter, handleRegister(db)))
	mux.HandleFunc("POST /auth/login", rateLimitMiddleware(loginLimiter, handleLogin(db)))

	// Auth routes (admin only — JWT validated inline)
	mux.HandleFunc("GET /auth/pending", handlePendingUsers(db))
	mux.HandleFunc("POST /auth/approve/{userId}", handleApproveUser(db))
	mux.HandleFunc("POST /auth/reject/{userId}", handleRejectUser(db))
	mux.HandleFunc("GET /auth/users", handleActiveUsers(db))

	// WebSocket endpoint (JWT via query param)
	mux.HandleFunc("/ws", handleWebSocket())

	// Internal service routes (no auth — accessed by other services via Docker network)
	mux.HandleFunc("GET /internal/contacts", handleListContacts(db))
	// Temp link issuance: /api/links/temp is admin-only (proxied externally);
	// /internal/links/temp is the unauthenticated Docker-internal path (notifier).
	mux.HandleFunc("POST /api/links/temp", handleCreateTempLink(db))
	mux.HandleFunc("POST /internal/links/temp", handleInternalCreateTempLink(db))
	mux.HandleFunc("GET /api/links/verify/{token}", handleVerifyTempLink())

	// Internal cameras list (no auth — for cctv-adapter reload)
	mux.HandleFunc("GET /internal/cameras", handleInternalListCameras(db))

	// Internal settings (no auth — for other services via Docker network)
	mux.HandleFunc("GET /internal/settings/{key}", handleInternalGetSetting(db))

	// Public invitation verification (no auth — for registration page)
	mux.HandleFunc("GET /api/invitations/verify/{token}", handleVerifyInvitation(db))

	// Incident creation (internal — from hw-gateway)
	mux.HandleFunc("POST /api/incidents", handleCreateIncident(db))

	// Device seen (internal — from hw-gateway on heartbeat/alert)
	mux.HandleFunc("POST /api/devices/seen", handleSeenDevice(db))

	// Incident resolve from sensor (internal — from hw-gateway on MQTT alert/resolved with kind=sensor_button)
	mux.HandleFunc("POST /api/incidents/{id}/resolve-from-sensor", handleResolveIncidentFromSensor(db))

	// System alarm ingest (internal — from notifier when all notification channels fail)
	mux.HandleFunc("POST /internal/alarms", handleCreateSystemAlarm())

	// Protected API routes (JWT required)
	apiMux := http.NewServeMux()
	apiMux.HandleFunc("GET /api/healthz", func(w http.ResponseWriter, r *http.Request) {
		user := getAuthUser(r)
		writeJSON(w, http.StatusOK, map[string]any{
			"status": "ok",
			"userId": user.UserID,
			"role":   user.Role,
		})
	})

	// Contacts CRUD
	apiMux.HandleFunc("GET /api/contacts", handleListContacts(db))
	apiMux.HandleFunc("POST /api/contacts", handleCreateContact(db))
	apiMux.HandleFunc("PUT /api/contacts/{id}", handleUpdateContact(db))
	apiMux.HandleFunc("DELETE /api/contacts/{id}", handleDeleteContact(db))

	// Sites management
	apiMux.HandleFunc("GET /api/sites", handleListSites(db))
	apiMux.HandleFunc("PUT /api/sites/{id}", handleUpdateSite(db))

	// Cameras
	apiMux.HandleFunc("GET /api/cameras", handleListCameras(db))
	apiMux.HandleFunc("POST /api/cameras", handleCreateCamera(db))
	apiMux.HandleFunc("PUT /api/cameras/{id}", handleUpdateCamera(db))
	apiMux.HandleFunc("DELETE /api/cameras/{id}", handleDeleteCamera(db))

	// Auth (authenticated user)
	apiMux.HandleFunc("POST /api/auth/change-password", handleChangePassword(db))

	// Invitations (admin only)
	apiMux.HandleFunc("POST /api/invitations", handleCreateInvitation(db))
	apiMux.HandleFunc("GET /api/invitations", handleListInvitations(db))
	apiMux.HandleFunc("DELETE /api/invitations/{id}", handleDeleteInvitation(db))

	// Incidents (any authenticated user)
	apiMux.HandleFunc("GET /api/incidents", handleListIncidents(db))
	apiMux.HandleFunc("PATCH /api/incidents/{id}/acknowledge", handleAcknowledgeIncident(db))
	apiMux.HandleFunc("PATCH /api/incidents/{id}/resolve", handleResolveIncident(db))

	// Equipment restart (any authenticated user)
	apiMux.HandleFunc("POST /api/equipment/restart", handleEquipmentRestart(db))

	// Devices management
	apiMux.HandleFunc("GET /api/devices", handleListDevices(db))
	apiMux.HandleFunc("GET /api/devices/all", handleListDevices(db))
	apiMux.HandleFunc("PATCH /api/devices/{id}", handleUpdateDeviceAlias(db))
	apiMux.HandleFunc("DELETE /api/devices/{id}", handleDeleteDevice(db))
	apiMux.HandleFunc("POST /api/devices/{id}/restore", handleRestoreDevice(db))

	// Test alert simulation (admin only)
	apiMux.HandleFunc("POST /api/test-alert", handleTestAlertProxy())

	// Recordings (proxy to recording service)
	apiMux.HandleFunc("GET /api/recordings/{stream_key}/play", handleRecordingsProxy())
	apiMux.HandleFunc("GET /api/recordings/{stream_key}/segments/{filename}", handleRecordingSegmentProxy())
	apiMux.HandleFunc("GET /api/recordings/{stream_key}", handleRecordingsProxy())

	// Archives (proxy to recording service)
	apiMux.HandleFunc("GET /api/archives", handleArchivesProxy())
	apiMux.HandleFunc("POST /api/archives", handleArchivesProxy())
	apiMux.HandleFunc("DELETE /api/archives/incident/{incidentId}", handleArchiveIncidentDeleteProxy())
	apiMux.HandleFunc("DELETE /api/archives/{id}", handleArchivesProxy())
	apiMux.HandleFunc("GET /api/archives/{id}/download", handleArchiveDownloadProxy())

	// Storage stats (proxy to recording service)
	apiMux.HandleFunc("GET /api/storage", handleStorageProxy())

	// System settings (admin only)
	apiMux.HandleFunc("GET /api/settings", handleListSettings(db))
	apiMux.HandleFunc("PUT /api/settings/{key}", handleUpdateSetting(db))

	// Unified health monitoring (any authenticated user)
	apiMux.HandleFunc("GET /api/health", handleGetHealth(healthMonitor))
	apiMux.HandleFunc("GET /api/health/events", handleListHealthEvents(db))

	// Temporary links management (admin only)
	apiMux.HandleFunc("GET /api/links", handleListTempLinks())
	apiMux.HandleFunc("DELETE /api/links/{id}", handleRevokeTempLink())

	// Mount protected routes behind auth middleware
	mux.Handle("/api/", authMiddleware(apiMux))

	startLinkCleanup()

	srv := newHTTPServer(maxBytesMiddleware(mux))

	// Graceful shutdown (#40): on SIGTERM/SIGINT stop the health monitor and
	// drain in-flight HTTP requests via srv.Shutdown before exiting, instead of
	// dying instantly on docker stop with the monitor goroutine and in-flight
	// handlers cut off mid-work.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		<-sigCh
		log.Println("shutting down: stopping health monitor and draining HTTP...")
		healthMonitor.Stop()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := srv.Shutdown(ctx); err != nil {
			log.Printf("http shutdown: %v", err)
		}
	}()

	log.Println("web-backend listening on :8080")
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
}

// maxRequestBodyBytes caps request bodies (#41). JSON payloads here are tiny;
// 1 MB is generous while preventing memory exhaustion from oversized bodies sent
// to unauthenticated endpoints (/auth/register, /auth/login, /internal/*).
const maxRequestBodyBytes = 1 << 20 // 1 MB

// maxBytesMiddleware wraps every request body in an http.MaxBytesReader so a
// handler that decodes an oversized body gets an error (→ 400) instead of
// buffering unbounded data. GET/HEAD requests without a body are unaffected.
func maxBytesMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, maxRequestBodyBytes)
		next.ServeHTTP(w, r)
	})
}

// newHTTPServer builds the service HTTP server with hardened timeouts. Without
// them ReadHeaderTimeout/ReadTimeout/IdleTimeout default to 0 (unlimited) and a
// slow/malicious client can trickle headers or body to hold goroutines/sockets
// open indefinitely (Slowloris). WriteTimeout is deliberately left at 0
// (unlimited): this service proxies large archive/segment video downloads and a
// hard write deadline would truncate legitimate long transfers.
func newHTTPServer(handler http.Handler) *http.Server {
	return &http.Server{
		Addr:              ":8080",
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
}

func initDB(path string) (*sql.DB, error) {
	// Apply pragmas via the DSN so they run on EVERY pooled connection. Setting
	// them once with db.ExecContext only configures a single connection; other
	// connections in the pool would keep busy_timeout=0 and immediately return
	// SQLITE_BUSY (HTTP 500, lost writes) under concurrent writes.
	dsn := fmt.Sprintf(
		"%s?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(ON)&_pragma=wal_autocheckpoint(100)",
		path,
	)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}

	// WAL mode (enabled via the DSN above) permits multiple concurrent readers
	// alongside a single writer, and busy_timeout(5000) makes a contending
	// writer wait for the lock rather than fail with SQLITE_BUSY. Together these
	// keep concurrent POST /api/incidents lossless WITHOUT collapsing the pool
	// to one shared connection.
	//
	// A single-connection pool (SetMaxOpenConns(1)) is self-defeating: a
	// long-lived read cursor — e.g. the health monitor walking the devices
	// table — holds that one connection for the whole loop, so an interleaved
	// write on the same goroutine has no second connection to use and blocks
	// for busy_timeout before failing with "context deadline exceeded", while
	// every other DB-backed HTTP request stalls behind it. Allow a small pool
	// so readers and writers get independent connections.
	db.SetMaxOpenConns(8)
	db.SetMaxIdleConns(8)
	// A long-lived pooled reader connection can keep pinning an old WAL snapshot,
	// which prevents a TRUNCATE checkpoint from advancing and lets the -wal file
	// grow without bound under sustained writer+reader load. Recycling connections
	// on a short lifetime forces those readers to drop their snapshot so the
	// checkpoint can reclaim frames. (Idle time is also shortened so an idle reader
	// does not sit on a snapshot for minutes.)
	db.SetConnMaxLifetime(15 * time.Second)
	db.SetConnMaxIdleTime(20 * time.Second)

	ctx, cancel := dbCtx(context.Background())
	defer cancel()

	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, err
	}

	log.Printf("database initialized at %s", path)
	return db, nil
}

const (
	// walCheckpointInterval is the unconditional periodic TRUNCATE cadence — it
	// reclaims the -wal file once writes quiesce even if no size threshold fires.
	walCheckpointInterval = 1 * time.Second
	// walSizeCheckInterval is how often the -wal file size is polled for the
	// size-triggered fast path.
	walSizeCheckInterval = 250 * time.Millisecond
	// walSizeThreshold is the -wal byte size that triggers an immediate TRUNCATE
	// checkpoint, so a writer burst is reclaimed before it can push the file past
	// the fixed bound. Kept well under the 5 MB recommended ceiling.
	walSizeThreshold = int64(2 * 1024 * 1024)
)

// walDirty is set by every write handler (via markWALDirty) and cleared after a
// successful periodic TRUNCATE. It lets the 1s ticker SKIP the checkpoint at zero
// write load instead of touching the DB every second forever. It is only an
// optimization hint: the ticker also checkpoints whenever the -wal file is still
// non-empty (see startWALCheckpoint), so no unmarked write path can cause a
// missed checkpoint and the reclaim guarantee holds even if a handler forgets to
// mark. When in doubt we err toward checkpointing.
var walDirty atomic.Bool

// markWALDirty records that a write occurred since the last successful TRUNCATE
// checkpoint. Called from write handlers after a successful mutating statement.
func markWALDirty() { walDirty.Store(true) }

// startWALCheckpoint bounds and reclaims the -wal file. wal_autocheckpoint (DSN)
// only performs PASSIVE checkpoints, which never truncate the file and stall
// behind any reader still pinned to an old WAL snapshot; under sustained
// writer+reader load the -wal file can grow past its ceiling and never reclaim
// space after the load stops. Two goroutines drive TRUNCATE checkpoints (which
// move committed frames into the main DB and reset the WAL toward zero):
//
//  1. a 1s ticker — guarantees reclaim once writes quiesce. It is gated so that
//     at zero write load (no writes since the last TRUNCATE AND the -wal file
//     already reclaimed to empty) it skips the checkpoint entirely instead of
//     hammering the DB every second. The gate is deliberately conservative: it
//     fires on EITHER a write mark OR a non-empty -wal file, so a still-pinned
//     WAL snapshot (e.g. an idle pooled reader) keeps getting TRUNCATE attempts
//     until it is actually reclaimed to zero — preserving reclaim-under-idle-
//     readers. Only the provably-quiescent case (no marks + empty -wal) is
//     skipped, where a checkpoint would be a guaranteed no-op.
//  2. a 250ms size sampler — the moment the -wal file exceeds walSizeThreshold
//     it forces a TRUNCATE, so a writer burst is reclaimed before it overshoots
//     the bound (the timer alone cannot keep up with a burst). Left UNGATED: it
//     only fires when the file is already over threshold, which itself proves
//     writes occurred, so gating would add nothing but risk.
//
// Combined with short connection lifetimes (so readers release their WAL
// snapshot and let the checkpoint advance), this keeps the WAL within a fixed
// bound even under adversarial writer+reader concurrency. Every checkpoint is
// best-effort: a transient SQLITE_BUSY (a checkpoint racing an in-flight writer)
// is logged and retried on the next tick, never surfaced to a request.
func startWALCheckpoint(db *sql.DB, dbPath string) {
	walPath := dbPath + "-wal"
	checkpoint := func() {
		ctx, cancel := dbCtx(context.Background())
		if _, err := db.ExecContext(ctx, "PRAGMA wal_checkpoint(TRUNCATE)"); err != nil {
			log.Printf("wal checkpoint(TRUNCATE) error: %v", err)
		}
		cancel()
	}
	// walNeedsCheckpoint reports whether a periodic TRUNCATE could still do useful
	// work. Consuming the dirty mark first (Swap) means a write racing in during a
	// checkpoint re-arms the flag for the next tick. The -wal size is the
	// authoritative backstop: after a TRUNCATE the file is reset to 0 bytes, so a
	// non-empty -wal proves frames remain unreclaimed (either a fresh write not yet
	// marked, or a pinned snapshot the previous TRUNCATE could not advance past).
	// We keep checkpointing until it reaches zero.
	walNeedsCheckpoint := func() bool {
		if walDirty.Swap(false) {
			return true
		}
		fi, err := os.Stat(walPath)
		if err != nil {
			// No -wal file yet => nothing to reclaim. Any other stat error: err
			// toward checkpointing (a spurious TRUNCATE is harmless).
			return !os.IsNotExist(err)
		}
		return fi.Size() > 0
	}
	// Periodic gated TRUNCATE.
	go func() {
		ticker := time.NewTicker(walCheckpointInterval)
		defer ticker.Stop()
		for range ticker.C {
			if walNeedsCheckpoint() {
				checkpoint()
			}
		}
	}()
	// Size-triggered fast path (ungated).
	go func() {
		ticker := time.NewTicker(walSizeCheckInterval)
		defer ticker.Stop()
		for range ticker.C {
			if fi, err := os.Stat(walPath); err == nil && fi.Size() > walSizeThreshold {
				checkpoint()
			}
		}
	}()
}
