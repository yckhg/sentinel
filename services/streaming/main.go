package main

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

const hlsDir = "/tmp/hls"

var streamActiveTimeout = func() time.Duration {
	if v := os.Getenv("STREAM_ACTIVE_TIMEOUT"); v != "" {
		if secs, err := strconv.Atoi(v); err == nil && secs > 0 {
			return time.Duration(secs) * time.Second
		}
	}
	return 30 * time.Second
}()

type StreamInfo struct {
	CameraID  string `json:"cameraId"`
	StreamKey string `json:"streamKey"`
	HlsURL    string `json:"hlsUrl"`
	Active    bool   `json:"active"`
	StartedAt string `json:"startedAt"`
}

func main() {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"ok","service":"streaming"}`))
	})

	mux.HandleFunc("GET /api/streams", handleStreams)

	log.Println("streaming-api listening on :8081")
	if err := http.ListenAndServe(":8081", mux); err != nil {
		log.Fatal(err)
	}
}

func handleStreams(w http.ResponseWriter, r *http.Request) {
	streams := []StreamInfo{}

	entries, err := os.ReadDir(hlsDir)
	if err != nil {
		// No HLS directory yet — return empty list
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(streams)
		return
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		cameraID := entry.Name()
		playlistPath := filepath.Join(hlsDir, cameraID, "index.m3u8")

		info, err := os.Stat(playlistPath)
		if err != nil {
			continue
		}

		// Consider stream active if playlist was modified within the detection window
		active := time.Since(info.ModTime()) < streamActiveTimeout

		streams = append(streams, StreamInfo{
			CameraID:  cameraID,
			StreamKey: cameraID,
			HlsURL:    "/live/" + cameraID + "/index.m3u8",
			Active:    active,
			StartedAt: info.ModTime().UTC().Format(time.RFC3339),
		})
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(streams)
}
