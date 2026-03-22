package main

import (
	"context"
	"encoding/json"
	"fmt"
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

		log.Printf("[%s] Resolving stream URL via yt-dlp for %s", src.ID, src.YouTubeURL)

		streamURL, err := resolveStreamURL(src.YouTubeURL)
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

		rtmpDest := fmt.Sprintf("%s/%s", m.rtmpURL, src.StreamKey)
		log.Printf("[%s] Starting FFmpeg stream to %s", src.ID, rtmpDest)

		state.Lock()
		state.status = "running"
		state.startedAt = time.Now()
		state.loopCount++
		state.Unlock()

		err = runFFmpeg(streamURL, rtmpDest, state.stopCh)

		select {
		case <-state.stopCh:
			state.Lock()
			state.status = "stopped"
			state.Unlock()
			return
		default:
		}

		if err != nil {
			log.Printf("[%s] FFmpeg exited with error: %v", src.ID, err)
			state.Lock()
			state.status = "error"
			state.lastError = fmt.Sprintf("ffmpeg: %v", err)
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

// runFFmpeg streams from sourceURL to rtmpDest
func runFFmpeg(sourceURL, rtmpDest string, stopCh chan struct{}) error {
	cmd := exec.Command("ffmpeg",
		"-hide_banner",
		"-loglevel", "warning",
		"-re",
		"-i", sourceURL,
		"-c", "copy",
		"-f", "flv",
		"-flvflags", "no_duration_filesize",
		rtmpDest,
	)
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

func main() {
	configPath := os.Getenv("YOUTUBE_CONFIG_PATH")
	if configPath == "" {
		configPath = "/config/youtube-sources.json"
	}

	rtmpURL := os.Getenv("STREAMING_RTMP_URL")
	if rtmpURL == "" {
		rtmpURL = "rtmp://streaming:1935/live"
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
