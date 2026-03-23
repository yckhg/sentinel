package main

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"
)

var (
	streamingURL      string
	cctvAdapterURL    string
	youtubeAdapterURL string
	recordingURL      string
)

func initServiceURLs() {
	streamingURL = os.Getenv("STREAMING_URL")
	if streamingURL == "" {
		streamingURL = "http://streaming:8080"
	}
	cctvAdapterURL = os.Getenv("CCTV_ADAPTER_URL")
	if cctvAdapterURL == "" {
		cctvAdapterURL = "http://cctv-adapter:8080"
	}
	youtubeAdapterURL = os.Getenv("YOUTUBE_ADAPTER_URL")
	if youtubeAdapterURL == "" {
		youtubeAdapterURL = "http://youtube-adapter:8080"
	}
	recordingURL = os.Getenv("RECORDING_URL")
	if recordingURL == "" {
		recordingURL = "http://recording:8080"
	}
	log.Printf("streaming URL: %s", streamingURL)
	log.Printf("cctv-adapter URL: %s", cctvAdapterURL)
	log.Printf("youtube-adapter URL: %s", youtubeAdapterURL)
	log.Printf("recording URL: %s", recordingURL)
}

// Cached responses from internal services
type streamCache struct {
	mu          sync.RWMutex
	streams     []streamInfo
	statuses    []cameraStatus
	streamsTTL  time.Time
	statusesTTL time.Time
}

type streamInfo struct {
	CameraID  string `json:"cameraId"`
	HLSUrl    string `json:"hlsUrl"`
	Active    bool   `json:"active"`
	StartedAt string `json:"startedAt"`
}

type cameraStatus struct {
	CameraID    string  `json:"cameraId"`
	Status      string  `json:"status"`
	ConnectedAt *string `json:"connectedAt"`
	LastError   *string `json:"lastError"`
}

type cameraResponse struct {
	ID         int64  `json:"id"`
	Name       string `json:"name"`
	Location   string `json:"location"`
	Zone       string `json:"zone"`
	StreamKey  string `json:"streamKey"`
	SourceType string `json:"sourceType"`
	SourceURL  string `json:"sourceUrl"`
	Enabled    bool   `json:"enabled"`
	HLSUrl     string `json:"hlsUrl"`
	Status     string `json:"status"`
}

type cameraRequest struct {
	Name       string `json:"name"`
	Location   string `json:"location"`
	Zone       string `json:"zone"`
	SourceType string `json:"sourceType"`
	SourceURL  string `json:"sourceUrl"`
	Enabled    *bool  `json:"enabled"`
}

// generateStreamKey creates a unique stream key in the format cam-{8 hex chars}.
func generateStreamKey() string {
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		log.Printf("crypto/rand failed: %v", err)
		// fallback: should never happen
		return fmt.Sprintf("cam-%08x", 0)
	}
	return "cam-" + hex.EncodeToString(b)
}

var cache = &streamCache{}

const cacheTTL = 10 * time.Second

func (c *streamCache) getStreams(client *http.Client) []streamInfo {
	c.mu.RLock()
	if time.Now().Before(c.streamsTTL) {
		result := c.streams
		c.mu.RUnlock()
		return result
	}
	c.mu.RUnlock()

	// Fetch fresh data
	resp, err := client.Get(streamingURL + "/api/streams")
	if err != nil {
		log.Printf("fetch streams error: %v", err)
		return nil
	}
	defer resp.Body.Close()

	var streams []streamInfo
	if err := json.NewDecoder(resp.Body).Decode(&streams); err != nil {
		log.Printf("decode streams error: %v", err)
		return nil
	}

	c.mu.Lock()
	c.streams = streams
	c.streamsTTL = time.Now().Add(cacheTTL)
	c.mu.Unlock()

	return streams
}

func (c *streamCache) getStatuses(client *http.Client) []cameraStatus {
	c.mu.RLock()
	if time.Now().Before(c.statusesTTL) {
		result := c.statuses
		c.mu.RUnlock()
		return result
	}
	c.mu.RUnlock()

	resp, err := client.Get(cctvAdapterURL + "/api/cameras/status")
	if err != nil {
		log.Printf("fetch camera statuses error: %v", err)
		return nil
	}
	defer resp.Body.Close()

	var statuses []cameraStatus
	if err := json.NewDecoder(resp.Body).Decode(&statuses); err != nil {
		log.Printf("decode camera statuses error: %v", err)
		return nil
	}

	c.mu.Lock()
	c.statuses = statuses
	c.statusesTTL = time.Now().Add(cacheTTL)
	c.mu.Unlock()

	return statuses
}

func scanCamera(rows interface{ Scan(dest ...any) error }) (cameraResponse, error) {
	var c cameraResponse
	var enabled int
	err := rows.Scan(&c.ID, &c.Name, &c.Location, &c.Zone, &c.StreamKey, &c.SourceType, &c.SourceURL, &enabled)
	c.Enabled = enabled != 0
	return c, err
}

func handleListCameras(db *sql.DB) http.HandlerFunc {
	client := &http.Client{Timeout: 5 * time.Second}

	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := dbCtx(r.Context())
		defer cancel()

		rows, err := db.QueryContext(ctx,
			"SELECT id, name, location, zone, stream_key, source_type, source_url, enabled FROM cameras ORDER BY id ASC")
		if err != nil {
			log.Printf("query cameras error: %v", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
			return
		}
		defer rows.Close()

		cameras := []cameraResponse{}
		for rows.Next() {
			c, err := scanCamera(rows)
			if err != nil {
				log.Printf("scan camera error: %v", err)
				continue
			}
			cameras = append(cameras, c)
		}

		// Fetch active streams from streaming server (single source of truth)
		streams := cache.getStreams(client)

		streamMap := make(map[string]streamInfo)
		for _, s := range streams {
			streamMap[s.CameraID] = s
		}

		for i := range cameras {
			if s, ok := streamMap[cameras[i].StreamKey]; ok && s.Active {
				cameras[i].HLSUrl = s.HLSUrl
				cameras[i].Status = "connected"
			} else {
				cameras[i].Status = "disconnected"
			}
		}

		writeJSON(w, http.StatusOK, cameras)
	}
}

func validateSourceType(st string) bool {
	return st == "rtsp" || st == "youtube"
}

var youtubeURLPattern = regexp.MustCompile(`^https://(www\.)?youtube\.com/watch\?v=[\w-]+|^https://youtu\.be/[\w-]+`)

// validateSourceURL checks that the camera source URL is safe (no SSRF).
// Returns an error message if invalid, or empty string if valid.
func validateSourceURL(sourceType, sourceURL string) string {
	if sourceURL == "" {
		return ""
	}

	switch sourceType {
	case "rtsp":
		if !strings.HasPrefix(sourceURL, "rtsp://") && !strings.HasPrefix(sourceURL, "rtsps://") {
			return "RTSP source URL must start with rtsp:// or rtsps://"
		}
	case "youtube":
		if !youtubeURLPattern.MatchString(sourceURL) {
			return "YouTube source URL must match https://(www.)youtube.com/watch?v=... or https://youtu.be/..."
		}
	}

	// Extract hostname for SSRF check
	hostname := extractHostname(sourceURL)
	if hostname == "" {
		return "unable to parse hostname from source URL"
	}

	if isPrivateHost(hostname) {
		return "source URL must not point to internal/private network addresses"
	}

	return ""
}

// extractHostname parses the hostname from a URL, handling both standard and rtsp:// schemes.
func extractHostname(rawURL string) string {
	// For rtsp(s):// URLs, temporarily replace scheme so net/url can parse
	normalized := rawURL
	if strings.HasPrefix(rawURL, "rtsp://") {
		normalized = "http://" + rawURL[len("rtsp://"):]
	} else if strings.HasPrefix(rawURL, "rtsps://") {
		normalized = "https://" + rawURL[len("rtsps://"):]
	}

	u, err := url.Parse(normalized)
	if err != nil {
		return ""
	}
	return u.Hostname()
}

// isPrivateHost checks if a hostname resolves to or represents a private/loopback address.
func isPrivateHost(hostname string) bool {
	// Direct string checks for common private patterns
	lower := strings.ToLower(hostname)
	if lower == "localhost" || lower == "[::1]" {
		return true
	}

	ip := net.ParseIP(hostname)
	if ip == nil {
		// Not a raw IP — could be a domain. For safety, only block known patterns.
		return false
	}

	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast()
}

func handleCreateCamera(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user := getAuthUser(r)
		if user.Role != "admin" {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "admin access required"})
			return
		}

		var req cameraRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
			return
		}

		req.Name = strings.TrimSpace(req.Name)
		req.Location = strings.TrimSpace(req.Location)
		req.Zone = strings.TrimSpace(req.Zone)
		req.SourceType = strings.TrimSpace(req.SourceType)
		req.SourceURL = strings.TrimSpace(req.SourceURL)

		if req.Name == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "name is required"})
			return
		}

		streamKey := generateStreamKey()
		if req.SourceType == "" {
			req.SourceType = "rtsp"
		}
		if !validateSourceType(req.SourceType) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "sourceType must be 'rtsp' or 'youtube'"})
			return
		}
		if errMsg := validateSourceURL(req.SourceType, req.SourceURL); errMsg != "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": errMsg})
			return
		}

		enabled := 1
		if req.Enabled != nil && !*req.Enabled {
			enabled = 0
		}

		ctx, cancel := dbCtx(r.Context())
		defer cancel()

		result, err := db.ExecContext(ctx,
			"INSERT INTO cameras (name, location, zone, stream_key, source_type, source_url, enabled) VALUES (?, ?, ?, ?, ?, ?, ?)",
			req.Name, req.Location, req.Zone, streamKey, req.SourceType, req.SourceURL, enabled,
		)
		if err != nil {
			log.Printf("insert camera error: %v", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
			return
		}

		id, _ := result.LastInsertId()
		log.Printf("camera created: id=%d name=%s streamKey=%s by user=%d", id, req.Name, streamKey, user.UserID)
		triggerCCTVReload()
		triggerYouTubeReload()

		writeJSON(w, http.StatusCreated, cameraResponse{
			ID:         id,
			Name:       req.Name,
			Location:   req.Location,
			Zone:       req.Zone,
			StreamKey:  streamKey,
			SourceType: req.SourceType,
			SourceURL:  req.SourceURL,
			Enabled:    enabled != 0,
		})
	}
}

func handleUpdateCamera(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user := getAuthUser(r)
		if user.Role != "admin" {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "admin access required"})
			return
		}

		idStr := r.PathValue("id")
		var id int64
		if _, err := fmt.Sscanf(idStr, "%d", &id); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid id"})
			return
		}

		var req cameraRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
			return
		}

		req.Name = strings.TrimSpace(req.Name)
		req.Location = strings.TrimSpace(req.Location)
		req.Zone = strings.TrimSpace(req.Zone)
		req.SourceType = strings.TrimSpace(req.SourceType)
		req.SourceURL = strings.TrimSpace(req.SourceURL)

		ctx, cancel := dbCtx(r.Context())
		defer cancel()

		// Load existing camera
		var existing cameraResponse
		var enabledInt int
		err := db.QueryRowContext(ctx,
			"SELECT id, name, location, zone, stream_key, source_type, source_url, enabled FROM cameras WHERE id = ?", id,
		).Scan(&existing.ID, &existing.Name, &existing.Location, &existing.Zone,
			&existing.StreamKey, &existing.SourceType, &existing.SourceURL, &enabledInt)
		if err == sql.ErrNoRows {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "camera not found"})
			return
		}
		if err != nil {
			log.Printf("query camera error: %v", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
			return
		}
		existing.Enabled = enabledInt != 0

		// Apply partial updates (stream_key is immutable — never changed after creation)
		if req.Name != "" {
			existing.Name = req.Name
		}
		if req.Location != "" {
			existing.Location = req.Location
		}
		if req.Zone != "" {
			existing.Zone = req.Zone
		}
		if req.SourceType != "" {
			if !validateSourceType(req.SourceType) {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "sourceType must be 'rtsp' or 'youtube'"})
				return
			}
			existing.SourceType = req.SourceType
		}
		if req.SourceURL != "" {
			existing.SourceURL = req.SourceURL
		}
		// Validate source URL after all partial updates are applied
		if errMsg := validateSourceURL(existing.SourceType, existing.SourceURL); errMsg != "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": errMsg})
			return
		}
		if req.Enabled != nil {
			existing.Enabled = *req.Enabled
		}

		enabledVal := 0
		if existing.Enabled {
			enabledVal = 1
		}

		_, err = db.ExecContext(ctx,
			"UPDATE cameras SET name = ?, location = ?, zone = ?, stream_key = ?, source_type = ?, source_url = ?, enabled = ? WHERE id = ?",
			existing.Name, existing.Location, existing.Zone, existing.StreamKey, existing.SourceType, existing.SourceURL, enabledVal, id,
		)
		if err != nil {
			log.Printf("update camera error: %v", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
			return
		}

		log.Printf("camera updated: id=%d by user=%d", id, user.UserID)
		triggerCCTVReload()
		triggerYouTubeReload()
		writeJSON(w, http.StatusOK, existing)
	}
}

// handleInternalListCameras returns all cameras for internal service-to-service use (no auth).
func handleInternalListCameras(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := dbCtx(r.Context())
		defer cancel()

		rows, err := db.QueryContext(ctx,
			"SELECT id, name, location, zone, stream_key, source_type, source_url, enabled FROM cameras ORDER BY id ASC")
		if err != nil {
			log.Printf("query cameras (internal) error: %v", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
			return
		}
		defer rows.Close()

		cameras := []cameraResponse{}
		for rows.Next() {
			c, err := scanCamera(rows)
			if err != nil {
				log.Printf("scan camera error: %v", err)
				continue
			}
			cameras = append(cameras, c)
		}

		writeJSON(w, http.StatusOK, cameras)
	}
}

// triggerCCTVReload calls cctv-adapter's reload endpoint asynchronously.
func triggerCCTVReload() {
	go func() {
		client := &http.Client{Timeout: 10 * time.Second}
		req, err := http.NewRequest("POST", cctvAdapterURL+"/api/cameras/reload", nil)
		if err != nil {
			log.Printf("cctv reload: failed to create request: %v", err)
			return
		}
		resp, err := client.Do(req)
		if err != nil {
			log.Printf("cctv reload: failed to call cctv-adapter: %v", err)
			return
		}
		defer resp.Body.Close()
		if resp.StatusCode == http.StatusOK {
			log.Printf("cctv reload: cctv-adapter reloaded successfully")
		} else {
			log.Printf("cctv reload: cctv-adapter returned status %d", resp.StatusCode)
		}
	}()
}

// triggerYouTubeReload calls youtube-adapter's reload endpoint asynchronously.
func triggerYouTubeReload() {
	go func() {
		client := &http.Client{Timeout: 10 * time.Second}
		req, err := http.NewRequest("POST", youtubeAdapterURL+"/api/cameras/reload", nil)
		if err != nil {
			log.Printf("youtube reload: failed to create request: %v", err)
			return
		}
		resp, err := client.Do(req)
		if err != nil {
			log.Printf("youtube reload: failed to call youtube-adapter: %v", err)
			return
		}
		defer resp.Body.Close()
		if resp.StatusCode == http.StatusOK {
			log.Printf("youtube reload: youtube-adapter reloaded successfully")
		} else {
			log.Printf("youtube reload: youtube-adapter returned status %d", resp.StatusCode)
		}
	}()
}

func handleDeleteCamera(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user := getAuthUser(r)
		if user.Role != "admin" {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "admin access required"})
			return
		}

		idStr := r.PathValue("id")
		var id int64
		if _, err := fmt.Sscanf(idStr, "%d", &id); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid id"})
			return
		}

		ctx, cancel := dbCtx(r.Context())
		defer cancel()

		result, err := db.ExecContext(ctx, "DELETE FROM cameras WHERE id = ?", id)
		if err != nil {
			log.Printf("delete camera error: %v", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
			return
		}

		rowsAffected, _ := result.RowsAffected()
		if rowsAffected == 0 {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "camera not found"})
			return
		}

		log.Printf("camera deleted: id=%d by user=%d", id, user.UserID)
		triggerCCTVReload()
		triggerYouTubeReload()
		w.WriteHeader(http.StatusNoContent)
	}
}
