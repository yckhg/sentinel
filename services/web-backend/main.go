package main

import (
	"database/sql"
	"log"
	"net/http"
	"os"

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

	if err := seedAdminUser(db); err != nil {
		log.Fatalf("failed to seed admin user: %v", err)
	}

	mux := http.NewServeMux()

	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"ok","service":"web-backend"}`))
	})

	// Auth routes (public)
	mux.HandleFunc("POST /auth/register", handleRegister(db))
	mux.HandleFunc("POST /auth/login", handleLogin(db))

	// Protected API routes (JWT required)
	apiMux := http.NewServeMux()
	// Placeholder for future /api/* handlers
	apiMux.HandleFunc("GET /api/healthz", func(w http.ResponseWriter, r *http.Request) {
		user := getAuthUser(r)
		writeJSON(w, http.StatusOK, map[string]any{
			"status": "ok",
			"userId": user.UserID,
			"role":   user.Role,
		})
	})

	// Mount protected routes behind auth middleware
	mux.Handle("/api/", authMiddleware(apiMux))

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

	// SQLite pragmas for performance and safety
	pragmas := []string{
		"PRAGMA journal_mode=WAL",
		"PRAGMA busy_timeout=5000",
		"PRAGMA foreign_keys=ON",
	}
	for _, p := range pragmas {
		if _, err := db.Exec(p); err != nil {
			db.Close()
			return nil, err
		}
	}

	if err := db.Ping(); err != nil {
		db.Close()
		return nil, err
	}

	log.Printf("database initialized at %s", path)
	return db, nil
}
