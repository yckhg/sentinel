package main

import (
	"context"
	"database/sql"
	"log"
	"net/http"
	"os"
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

	if err := seedAdminUser(db); err != nil {
		log.Fatalf("failed to seed admin user: %v", err)
	}

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
	mux.HandleFunc("GET /api/contacts", handleListContacts(db))
	mux.HandleFunc("POST /api/links/temp", handleCreateTempLink())
	mux.HandleFunc("GET /api/links/verify/{token}", handleVerifyTempLink())

	// Internal cameras list (no auth — for cctv-adapter reload)
	mux.HandleFunc("GET /internal/cameras", handleInternalListCameras(db))

	// Public invitation verification (no auth — for registration page)
	mux.HandleFunc("GET /api/invitations/verify/{token}", handleVerifyInvitation(db))

	// Incident creation (internal — from hw-gateway)
	mux.HandleFunc("POST /api/incidents", handleCreateIncident(db))

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

	// Equipment restart (any authenticated user)
	apiMux.HandleFunc("POST /api/equipment/restart", handleEquipmentRestart())

	// Temporary links management (admin only)
	apiMux.HandleFunc("GET /api/links", handleListTempLinks())
	apiMux.HandleFunc("DELETE /api/links/{id}", handleRevokeTempLink())

	// Mount protected routes behind auth middleware
	mux.Handle("/api/", authMiddleware(apiMux))

	startLinkCleanup()

	log.Println("web-backend listening on :8080")
	if err := http.ListenAndServe(":8080", mux); err != nil {
		log.Fatal(err)
	}
}

func initDB(path string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}

	ctx, cancel := dbCtx(context.Background())
	defer cancel()

	// SQLite pragmas for performance and safety
	pragmas := []string{
		"PRAGMA journal_mode=WAL",
		"PRAGMA busy_timeout=5000",
		"PRAGMA foreign_keys=ON",
	}
	for _, p := range pragmas {
		if _, err := db.ExecContext(ctx, p); err != nil {
			db.Close()
			return nil, err
		}
	}

	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, err
	}

	log.Printf("database initialized at %s", path)
	return db, nil
}
