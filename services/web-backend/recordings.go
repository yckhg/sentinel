package main

import (
	"io"
	"log"
	"net/http"
	"strings"
	"time"
)

// handleRecordingsProxy proxies recording API requests to the recording service.
// Handles both /api/recordings/{stream_key} and /api/recordings/{stream_key}/play
func handleRecordingsProxy() http.HandlerFunc {
	client := &http.Client{Timeout: 15 * time.Second}

	return func(w http.ResponseWriter, r *http.Request) {
		// Build target URL: forward the full path and query string to recording service
		targetURL := recordingURL + r.URL.Path
		if r.URL.RawQuery != "" {
			targetURL += "?" + r.URL.RawQuery
		}

		resp, err := client.Get(targetURL)
		if err != nil {
			log.Printf("recording proxy error: %v", err)
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": "failed to reach recording service"})
			return
		}
		defer resp.Body.Close()

		// Forward response headers
		for key, values := range resp.Header {
			for _, v := range values {
				w.Header().Add(key, v)
			}
		}

		// Rewrite segment URLs in playlist to go through web-backend proxy
		if strings.Contains(resp.Header.Get("Content-Type"), "mpegurl") {
			body, err := io.ReadAll(resp.Body)
			if err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to read recording response"})
				return
			}
			w.WriteHeader(resp.StatusCode)
			w.Write(body)
			return
		}

		w.WriteHeader(resp.StatusCode)
		io.Copy(w, resp.Body)
	}
}

// handleRecordingSegmentProxy proxies segment file requests to the recording service.
func handleRecordingSegmentProxy() http.HandlerFunc {
	client := &http.Client{Timeout: 30 * time.Second}

	return func(w http.ResponseWriter, r *http.Request) {
		targetURL := recordingURL + r.URL.Path

		resp, err := client.Get(targetURL)
		if err != nil {
			log.Printf("recording segment proxy error: %v", err)
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": "failed to reach recording service"})
			return
		}
		defer resp.Body.Close()

		for key, values := range resp.Header {
			for _, v := range values {
				w.Header().Add(key, v)
			}
		}

		w.WriteHeader(resp.StatusCode)
		io.Copy(w, resp.Body)
	}
}

// handleArchivesProxy proxies archive API requests to the recording service.
// Supports GET /api/archives, POST /api/archives, DELETE /api/archives/{id}
func handleArchivesProxy() http.HandlerFunc {
	client := &http.Client{Timeout: 30 * time.Second}

	return func(w http.ResponseWriter, r *http.Request) {
		targetURL := recordingURL + r.URL.Path
		if r.URL.RawQuery != "" {
			targetURL += "?" + r.URL.RawQuery
		}

		var req *http.Request
		var err error

		if r.Method == http.MethodPost {
			req, err = http.NewRequest(r.Method, targetURL, r.Body)
		} else {
			req, err = http.NewRequest(r.Method, targetURL, nil)
		}
		if err != nil {
			log.Printf("archives proxy request error: %v", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
			return
		}
		req.Header.Set("Content-Type", "application/json")

		resp, err := client.Do(req)
		if err != nil {
			log.Printf("archives proxy error: %v", err)
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": "failed to reach recording service"})
			return
		}
		defer resp.Body.Close()

		for key, values := range resp.Header {
			for _, v := range values {
				w.Header().Add(key, v)
			}
		}

		w.WriteHeader(resp.StatusCode)
		io.Copy(w, resp.Body)
	}
}

// handleArchiveIncidentDeleteProxy proxies incident-level archive delete requests to the recording service.
func handleArchiveIncidentDeleteProxy() http.HandlerFunc {
	client := &http.Client{Timeout: 30 * time.Second}

	return func(w http.ResponseWriter, r *http.Request) {
		targetURL := recordingURL + r.URL.Path

		req, err := http.NewRequest(http.MethodDelete, targetURL, nil)
		if err != nil {
			log.Printf("archive incident delete proxy error: %v", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
			return
		}

		resp, err := client.Do(req)
		if err != nil {
			log.Printf("archive incident delete proxy error: %v", err)
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": "failed to reach recording service"})
			return
		}
		defer resp.Body.Close()

		for key, values := range resp.Header {
			for _, v := range values {
				w.Header().Add(key, v)
			}
		}

		w.WriteHeader(resp.StatusCode)
		io.Copy(w, resp.Body)
	}
}

// handleArchiveDownloadProxy proxies archive download requests to the recording service.
func handleArchiveDownloadProxy() http.HandlerFunc {
	client := &http.Client{Timeout: 120 * time.Second}

	return func(w http.ResponseWriter, r *http.Request) {
		targetURL := recordingURL + r.URL.Path

		resp, err := client.Get(targetURL)
		if err != nil {
			log.Printf("archive download proxy error: %v", err)
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": "failed to reach recording service"})
			return
		}
		defer resp.Body.Close()

		for key, values := range resp.Header {
			for _, v := range values {
				w.Header().Add(key, v)
			}
		}

		w.WriteHeader(resp.StatusCode)
		io.Copy(w, resp.Body)
	}
}

// handleStorageProxy proxies storage stats requests to the recording service.
func handleStorageProxy() http.HandlerFunc {
	client := &http.Client{Timeout: 15 * time.Second}

	return func(w http.ResponseWriter, r *http.Request) {
		targetURL := recordingURL + "/api/storage"

		resp, err := client.Get(targetURL)
		if err != nil {
			log.Printf("storage proxy error: %v", err)
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": "failed to reach recording service"})
			return
		}
		defer resp.Body.Close()

		for key, values := range resp.Header {
			for _, v := range values {
				w.Header().Add(key, v)
			}
		}

		w.WriteHeader(resp.StatusCode)
		io.Copy(w, resp.Body)
	}
}
