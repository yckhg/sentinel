package main

import (
	"database/sql"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"sync"
	"time"
)

var (
	streamingURL   string
	cctvAdapterURL string
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
	log.Printf("streaming URL: %s", streamingURL)
	log.Printf("cctv-adapter URL: %s", cctvAdapterURL)
}

// Cached responses from internal services
type streamCache struct {
	mu        sync.RWMutex
	streams   []streamInfo
	statuses  []cameraStatus
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
	ID       int64  `json:"id"`
	Name     string `json:"name"`
	Location string `json:"location"`
	Zone     string `json:"zone"`
	HLSUrl   string `json:"hlsUrl"`
	Status   string `json:"status"`
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

func handleListCameras(db *sql.DB) http.HandlerFunc {
	client := &http.Client{Timeout: 5 * time.Second}

	return func(w http.ResponseWriter, r *http.Request) {
		// 1. Query cameras from DB
		rows, err := db.Query("SELECT id, name, location, zone FROM cameras ORDER BY id ASC")
		if err != nil {
			log.Printf("query cameras error: %v", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
			return
		}
		defer rows.Close()

		cameras := []cameraResponse{}
		for rows.Next() {
			var c cameraResponse
			if err := rows.Scan(&c.ID, &c.Name, &c.Location, &c.Zone); err != nil {
				log.Printf("scan camera error: %v", err)
				continue
			}
			cameras = append(cameras, c)
		}

		// 2. Fetch streams and statuses from internal services (uses cache)
		streams := cache.getStreams(client)
		statuses := cache.getStatuses(client)

		// Build lookup maps
		streamMap := make(map[string]streamInfo)
		for _, s := range streams {
			streamMap[s.CameraID] = s
		}
		statusMap := make(map[string]string)
		for _, s := range statuses {
			statusMap[s.CameraID] = s.Status
		}

		// 3. Merge: enrich DB cameras with HLS URLs and statuses
		for i := range cameras {
			// Match by camera name (DB name used as cameraId in streaming/adapter config)
			if s, ok := streamMap[cameras[i].Name]; ok && s.Active {
				cameras[i].HLSUrl = streamingURL + s.HLSUrl
			}
			if status, ok := statusMap[cameras[i].Name]; ok {
				cameras[i].Status = status
			} else {
				cameras[i].Status = "disconnected"
			}
		}

		writeJSON(w, http.StatusOK, cameras)
	}
}
