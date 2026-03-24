package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"time"
)

var webBackendURL string

// apiCamera represents a camera from the web-backend API.
type apiCamera struct {
	ID         int64  `json:"id"`
	Name       string `json:"name"`
	StreamKey  string `json:"streamKey"`
	SourceType string `json:"sourceType"`
	SourceURL  string `json:"sourceUrl"`
	Enabled    bool   `json:"enabled"`
}

// youtubeURLPattern matches valid YouTube video URLs.
var youtubeURLPattern = regexp.MustCompile(
	`^https://(www\.)?youtube\.com/watch\?v=[\w-]+|^https://youtu\.be/[\w-]+`,
)

const maxYouTubeURLLength = 200

// validateYouTubeURL checks that the URL is a valid YouTube video URL.
func validateYouTubeURL(rawURL string) error {
	if len(rawURL) > maxYouTubeURLLength {
		return fmt.Errorf("URL exceeds maximum length of %d characters", maxYouTubeURLLength)
	}
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("invalid URL: %w", err)
	}
	if parsed.Scheme != "https" {
		return fmt.Errorf("URL must use https scheme, got %q", parsed.Scheme)
	}
	if !youtubeURLPattern.MatchString(rawURL) {
		return fmt.Errorf("URL must match https://(www.)youtube.com/watch?v=... or https://youtu.be/...")
	}
	return nil
}

// YouTubeSource represents a single YouTube video to stream
type YouTubeSource struct {
	ID         string `json:"id"`
	YouTubeURL string `json:"youtubeUrl"`
	StreamKey  string `json:"streamKey"`
	LocalFile  string `json:"localFile,omitempty"`
}

// StreamStatus tracks the runtime state of a stream
type StreamStatus struct {
	ID          string `json:"id"`
	StreamKey   string `json:"streamKey"`
	Status      string `json:"status"` // running, stopped, error, starting
	LastError   string `json:"lastError,omitempty"`
	StartedAt   string `json:"startedAt,omitempty"`
	LoopCount   int    `json:"loopCount"`
}

type streamState struct {
	sync.RWMutex
	status    string
	lastError string
	startedAt time.Time
	loopCount int
	stopCh    chan struct{}
}

// StreamManager manages all YouTube stream processes
type StreamManager struct {
	mu      sync.RWMutex
	streams map[string]*streamState
	sources []YouTubeSource
	rtmpURL string
}

func NewStreamManager(sources []YouTubeSource, rtmpURL string) *StreamManager {
	return &StreamManager{
		streams: make(map[string]*streamState),
		sources: sources,
		rtmpURL: rtmpURL,
	}
}

func (m *StreamManager) StartAll() {
	for _, src := range m.sources {
		m.startStream(src)
	}
}

func (m *StreamManager) StopAll() {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, state := range m.streams {
		close(state.stopCh)
	}
}

// Reload reconciles running streams with a new set of sources.
// Unchanged streams (same ID + same YouTubeURL) keep running.
func (m *StreamManager) Reload(newSources []YouTubeSource) {
	m.mu.Lock()

	oldByID := make(map[string]YouTubeSource)
	for _, src := range m.sources {
		oldByID[src.ID] = src
	}

	newByID := make(map[string]YouTubeSource)
	for _, src := range newSources {
		newByID[src.ID] = src
	}

	// Stop removed or changed streams
	for id, old := range oldByID {
		newSrc, exists := newByID[id]
		if !exists || newSrc.YouTubeURL != old.YouTubeURL {
			if state, ok := m.streams[id]; ok {
				log.Printf("[%s] Stopping stream (removed or changed)", id)
				close(state.stopCh)
				delete(m.streams, id)
			}
		}
	}

	// Identify streams to start
	var toStart []YouTubeSource
	for id, src := range newByID {
		old, exists := oldByID[id]
		if !exists || old.YouTubeURL != src.YouTubeURL {
			toStart = append(toStart, src)
		}
	}

	m.sources = newSources
	m.mu.Unlock()

	for _, src := range toStart {
		log.Printf("[%s] Starting stream", src.ID)
		m.startStream(src)
	}
}

func (m *StreamManager) startStream(src YouTubeSource) {
	state := &streamState{
		status: "starting",
		stopCh: make(chan struct{}),
	}

	m.mu.Lock()
	m.streams[src.ID] = state
	m.mu.Unlock()

	go m.manageStream(src, state)
}

func (m *StreamManager) manageStream(src YouTubeSource, state *streamState) {
	backoff := time.Second
	maxBackoff := 30 * time.Second

	for {
		select {
		case <-state.stopCh:
			state.Lock()
			state.status = "stopped"
			state.Unlock()
			return
		default:
		}

		var streamURL string
		if src.LocalFile != "" {
			// Use local file — no yt-dlp needed
			log.Printf("[%s] Using local file: %s", src.ID, src.LocalFile)
			streamURL = src.LocalFile
		} else {
			log.Printf("[%s] Resolving stream URL via yt-dlp for %s", src.ID, src.YouTubeURL)
			var err error
			streamURL, err = resolveStreamURL(src.YouTubeURL)
			if err != nil {
				log.Printf("[%s] yt-dlp error: %v", src.ID, err)
				state.Lock()
				state.status = "error"
				state.lastError = fmt.Sprintf("yt-dlp: %v", err)
				state.Unlock()

				select {
				case <-state.stopCh:
					return
				case <-time.After(backoff):
					backoff = min(backoff*2, maxBackoff)
					continue
				}
			}
		}

		rtmpDest := fmt.Sprintf("%s/%s", m.rtmpURL, src.StreamKey)
		log.Printf("[%s] Starting FFmpeg stream to %s", src.ID, rtmpDest)

		state.Lock()
		state.status = "running"
		state.startedAt = time.Now()
		state.loopCount++
		state.Unlock()

		ffErr := runFFmpeg(streamURL, rtmpDest, src.LocalFile != "", state.stopCh)

		select {
		case <-state.stopCh:
			state.Lock()
			state.status = "stopped"
			state.Unlock()
			return
		default:
		}

		if ffErr != nil {
			log.Printf("[%s] FFmpeg exited with error: %v", src.ID, ffErr)
			state.Lock()
			state.status = "error"
			state.lastError = fmt.Sprintf("ffmpeg: %v", ffErr)
			state.Unlock()

			select {
			case <-state.stopCh:
				return
			case <-time.After(backoff):
				backoff = min(backoff*2, maxBackoff)
				continue
			}
		}

		// FFmpeg exited cleanly (video ended) — loop by re-resolving URL
		log.Printf("[%s] Stream ended, looping...", src.ID)
		backoff = time.Second // reset backoff on clean exit
	}
}

// resolveStreamURL uses yt-dlp to get the direct stream URL.
// It validates the YouTube URL before execution and enforces a 30s timeout.
func resolveStreamURL(youtubeURL string) (string, error) {
	if err := validateYouTubeURL(youtubeURL); err != nil {
		return "", fmt.Errorf("invalid YouTube URL: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "yt-dlp",
		"--no-warnings",
		"-f", "best[ext=mp4]/best",
		"--get-url",
		youtubeURL,
	)
	out, err := cmd.Output()
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return "", fmt.Errorf("yt-dlp timed out after 30s")
		}
		if exitErr, ok := err.(*exec.ExitError); ok {
			return "", fmt.Errorf("%v: %s", err, string(exitErr.Stderr))
		}
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// runFFmpeg streams from sourceURL to rtmpDest.
// Video is re-encoded with libx264 (no B-frames) because nginx-rtmp module v1.2.2
// drops connections when H.264 B-frame composition time offsets are present in FLV.
// Audio is re-encoded to AAC 48k for consistent FLV compatibility.
func runFFmpeg(sourceURL, rtmpDest string, isLocalFile bool, stopCh chan struct{}) error {
	args := []string{
		"-hide_banner",
		"-loglevel", "error",
		"-re",
	}
	if isLocalFile {
		args = append(args, "-stream_loop", "-1")
	}
	args = append(args,
		"-i", sourceURL,
		"-c:v", "libx264",
		"-preset", "ultrafast",
		"-tune", "zerolatency",
		"-b:v", "300k",
		"-g", "60",
		"-c:a", "aac",
		"-b:a", "48k",
		"-f", "flv",
		"-flvflags", "no_duration_filesize",
		rtmpDest,
	)
	cmd := exec.Command("ffmpeg", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start ffmpeg: %w", err)
	}

	// Monitor stop signal in background
	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
	}()

	select {
	case <-stopCh:
		// Graceful shutdown: SIGTERM then wait
		cmd.Process.Signal(syscall.SIGTERM)
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			cmd.Process.Kill()
			<-done
		}
		return nil
	case err := <-done:
		return err
	}
}

func (m *StreamManager) GetStatuses() []StreamStatus {
	m.mu.RLock()
	defer m.mu.RUnlock()

	statuses := make([]StreamStatus, 0, len(m.sources))
	for _, src := range m.sources {
		st := StreamStatus{
			ID:        src.ID,
			StreamKey: src.StreamKey,
			Status:    "unknown",
		}
		if state, ok := m.streams[src.ID]; ok {
			state.RLock()
			st.Status = state.status
			st.LastError = state.lastError
			st.LoopCount = state.loopCount
			if !state.startedAt.IsZero() {
				st.StartedAt = state.startedAt.Format(time.RFC3339)
			}
			state.RUnlock()
		}
		statuses = append(statuses, st)
	}
	return statuses
}

func loadConfig(path string) ([]YouTubeSource, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var sources []YouTubeSource
	if err := json.Unmarshal(data, &sources); err != nil {
		return nil, err
	}

	// Validate all YouTube URLs at config load time
	var valid []YouTubeSource
	for _, src := range sources {
		if err := validateYouTubeURL(src.YouTubeURL); err != nil {
			log.Printf("WARNING: Skipping source %q: invalid YouTube URL %q: %v", src.ID, src.YouTubeURL, err)
			continue
		}
		valid = append(valid, src)
	}
	return valid, nil
}

// fetchCamerasFromAPI fetches the camera list from web-backend.
func fetchCamerasFromAPI() ([]apiCamera, error) {
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(webBackendURL + "/internal/cameras")
	if err != nil {
		return nil, fmt.Errorf("fetch cameras: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("fetch cameras: status %d: %s", resp.StatusCode, string(body))
	}

	var cameras []apiCamera
	if err := json.NewDecoder(resp.Body).Decode(&cameras); err != nil {
		return nil, fmt.Errorf("decode cameras: %w", err)
	}
	return cameras, nil
}

// camerasToSources converts API cameras to YouTubeSource, filtered by youtube type.
func camerasToSources(cameras []apiCamera) []YouTubeSource {
	var sources []YouTubeSource
	for _, c := range cameras {
		if c.SourceType != "youtube" || !c.Enabled || c.StreamKey == "" {
			continue
		}
		sources = append(sources, YouTubeSource{
			ID:         c.StreamKey,
			YouTubeURL: c.SourceURL,
			StreamKey:  c.StreamKey,
		})
	}
	return sources
}

func main() {
	configPath := os.Getenv("YOUTUBE_CONFIG_PATH")
	if configPath == "" {
		configPath = "/config/youtube-sources.json"
	}

	rtmpURL := os.Getenv("STREAMING_RTMP_URL")
	if rtmpURL == "" {
		rtmpURL = "rtmp://streaming:1935/live"
	}

	webBackendURL = os.Getenv("WEB_BACKEND_URL")
	if webBackendURL == "" {
		webBackendURL = "http://web-backend:8080"
	}

	sources, err := loadConfig(configPath)
	if err != nil {
		log.Printf("WARNING: Could not load config from %s: %v", configPath, err)
		log.Printf("Starting with no streams configured")
		sources = []YouTubeSource{}
	}

	log.Printf("Loaded %d YouTube source(s)", len(sources))

	manager := NewStreamManager(sources, rtmpURL)
	manager.StartAll()

	// HTTP handlers
	http.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{
			"status":  "ok",
			"service": "youtube-adapter",
		})
	})

	http.HandleFunc("/api/streams/status", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(manager.GetStatuses())
	})

	http.HandleFunc("POST /api/cameras/reload", func(w http.ResponseWriter, r *http.Request) {
		cameras, err := fetchCamerasFromAPI()
		if err != nil {
			log.Printf("reload: failed to fetch cameras: %v", err)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}

		sources := camerasToSources(cameras)
		log.Printf("reload: %d youtube camera(s) from API", len(sources))
		manager.Reload(sources)

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"status": "ok",
			"count":  len(sources),
		})
	})

	// Graceful shutdown
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)

	go func() {
		<-sigCh
		log.Println("Shutting down...")
		manager.StopAll()
		os.Exit(0)
	}()

	log.Println("youtube-adapter listening on :8080")
	if err := http.ListenAndServe(":8080", nil); err != nil {
		log.Fatal(err)
	}
}
