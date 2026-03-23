package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

// CameraInfo represents a camera to record.
type CameraInfo struct {
	StreamKey string `json:"streamKey"`
	Name      string `json:"name"`
	Enabled   bool   `json:"enabled"`
}

// RecorderStatus tracks the runtime state of a recording process.
type RecorderStatus struct {
	StreamKey  string  `json:"streamKey"`
	Status     string  `json:"status"` // recording, disconnected, reconnecting
	StartedAt  *string `json:"startedAt"`
	LastError  *string `json:"lastError"`
	SegmentDir string  `json:"segmentDir"`
}

// watchdogWriter wraps an io.Writer and tracks the last time data was written.
type watchdogWriter struct {
	inner    *os.File
	lastSeen atomic.Int64
}

func newWatchdogWriter(f *os.File) *watchdogWriter {
	w := &watchdogWriter{inner: f}
	w.lastSeen.Store(time.Now().Unix())
	return w
}

func (w *watchdogWriter) Write(p []byte) (int, error) {
	w.lastSeen.Store(time.Now().Unix())
	return w.inner.Write(p)
}

// RecordingManager manages FFmpeg recording processes for all cameras.
type RecordingManager struct {
	mu            sync.RWMutex
	cameras       []CameraInfo
	states        map[string]*recorderState
	rtmpBaseURL   string
	recordingsDir string
	timeout       time.Duration

	// Protected segments (excluded from rolling cleanup)
	protectedMu sync.RWMutex
	protected   map[string]bool // full file path -> true
}

type recorderState struct {
	status    string
	startedAt *time.Time
	lastError *string
	cmd       *exec.Cmd
	stopCh    chan struct{}
}

func NewRecordingManager(rtmpBaseURL, recordingsDir string, timeout time.Duration) *RecordingManager {
	return &RecordingManager{
		states:        make(map[string]*recorderState),
		rtmpBaseURL:   rtmpBaseURL,
		recordingsDir: recordingsDir,
		timeout:       timeout,
		protected:     make(map[string]bool),
	}
}

// Start launches recording processes for all configured cameras.
func (rm *RecordingManager) Start(cameras []CameraInfo) {
	rm.mu.Lock()
	rm.cameras = cameras
	rm.mu.Unlock()

	for _, cam := range cameras {
		if !cam.Enabled {
			continue
		}
		rm.startRecorder(cam)
	}
}

func (rm *RecordingManager) startRecorder(cam CameraInfo) {
	state := &recorderState{
		status: "disconnected",
		stopCh: make(chan struct{}),
	}
	rm.mu.Lock()
	rm.states[cam.StreamKey] = state
	rm.mu.Unlock()

	go rm.manageRecording(cam, state)
}

// manageRecording runs FFmpeg to record RTMP stream as segmented .ts files.
func (rm *RecordingManager) manageRecording(cam CameraInfo, state *recorderState) {
	backoff := time.Second
	maxBackoff := 30 * time.Second

	// Ensure output directory exists
	outDir := filepath.Join(rm.recordingsDir, cam.StreamKey)
	os.MkdirAll(outDir, 0755)

	for {
		select {
		case <-state.stopCh:
			return
		default:
		}

		rm.mu.Lock()
		state.status = "reconnecting"
		rm.mu.Unlock()

		srcURL := fmt.Sprintf("%s/%s", rm.rtmpBaseURL, cam.StreamKey)
		segPattern := filepath.Join(outDir, "%Y%m%d_%H%M%S.ts")

		log.Printf("[%s] Connecting to RTMP stream: %s", cam.StreamKey, srcURL)

		// Record RTMP stream as segmented .ts files
		// -c copy: no transcoding (H.264 passthrough)
		// -f segment: output as individual segment files
		// -segment_time 10: 10-second segments
		// -strftime 1: use timestamp-based filenames
		// -segment_atclocktime 1: align segments to clock time
		// -reset_timestamps 1: reset timestamps per segment for clean playback
		cmd := exec.Command("ffmpeg",
			"-hide_banner",
			"-loglevel", "warning",
			"-i", srcURL,
			"-c", "copy",
			"-f", "segment",
			"-segment_time", "10",
			"-segment_format", "mpegts",
			"-strftime", "1",
			"-segment_atclocktime", "1",
			"-reset_timestamps", "1",
			segPattern,
		)

		stdoutWatcher := newWatchdogWriter(os.Stdout)
		stderrWatcher := newWatchdogWriter(os.Stderr)
		cmd.Stdout = stdoutWatcher
		cmd.Stderr = stderrWatcher

		rm.mu.Lock()
		state.cmd = cmd
		rm.mu.Unlock()

		err := cmd.Start()
		if err != nil {
			errMsg := fmt.Sprintf("Failed to start FFmpeg: %v", err)
			log.Printf("[%s] %s", cam.StreamKey, errMsg)
			rm.mu.Lock()
			state.status = "disconnected"
			state.lastError = &errMsg
			state.startedAt = nil
			rm.mu.Unlock()

			time.Sleep(backoff)
			backoff = min(backoff*2, maxBackoff)
			continue
		}

		now := time.Now().UTC()
		rm.mu.Lock()
		state.status = "recording"
		state.startedAt = &now
		state.lastError = nil
		rm.mu.Unlock()

		log.Printf("[%s] Recording started, segments → %s", cam.StreamKey, outDir)
		backoff = time.Second

		// Watchdog goroutine
		processDone := make(chan struct{})
		go func() {
			ticker := time.NewTicker(rm.timeout / 2)
			defer ticker.Stop()
			for {
				select {
				case <-ticker.C:
					lastStdout := time.Unix(stdoutWatcher.lastSeen.Load(), 0)
					lastStderr := time.Unix(stderrWatcher.lastSeen.Load(), 0)
					lastOutput := lastStdout
					if lastStderr.After(lastOutput) {
						lastOutput = lastStderr
					}
					if time.Since(lastOutput) > rm.timeout {
						log.Printf("[%s] FFmpeg output timeout (%v), stopping", cam.StreamKey, rm.timeout)
						if cmd.Process != nil {
							cmd.Process.Signal(syscall.SIGTERM)
							select {
							case <-time.After(5 * time.Second):
								cmd.Process.Kill()
							case <-processDone:
							}
						}
						return
					}
				case <-processDone:
					return
				case <-state.stopCh:
					return
				}
			}
		}()

		err = cmd.Wait()
		close(processDone)

		if err != nil {
			errMsg := fmt.Sprintf("FFmpeg exited: %v", err)
			log.Printf("[%s] %s", cam.StreamKey, errMsg)
			rm.mu.Lock()
			state.status = "disconnected"
			state.lastError = &errMsg
			state.startedAt = nil
			rm.mu.Unlock()
		} else {
			log.Printf("[%s] FFmpeg exited cleanly", cam.StreamKey)
			rm.mu.Lock()
			state.status = "disconnected"
			state.startedAt = nil
			rm.mu.Unlock()
		}

		select {
		case <-state.stopCh:
			return
		case <-time.After(backoff):
		}
		backoff = min(backoff*2, maxBackoff)
	}
}

// GetStatuses returns the current status of all recorders.
func (rm *RecordingManager) GetStatuses() []RecorderStatus {
	rm.mu.RLock()
	defer rm.mu.RUnlock()

	result := make([]RecorderStatus, 0, len(rm.cameras))
	for _, cam := range rm.cameras {
		if !cam.Enabled {
			continue
		}
		state, ok := rm.states[cam.StreamKey]
		if !ok {
			result = append(result, RecorderStatus{
				StreamKey:  cam.StreamKey,
				Status:     "disconnected",
				SegmentDir: filepath.Join(rm.recordingsDir, cam.StreamKey),
			})
			continue
		}

		rs := RecorderStatus{
			StreamKey:  cam.StreamKey,
			Status:     state.status,
			LastError:  state.lastError,
			SegmentDir: filepath.Join(rm.recordingsDir, cam.StreamKey),
		}
		if state.startedAt != nil {
			t := state.startedAt.Format(time.RFC3339)
			rs.StartedAt = &t
		}
		result = append(result, rs)
	}
	return result
}

// Reload reconciles running recorders with a new camera list.
func (rm *RecordingManager) Reload(newCameras []CameraInfo) {
	rm.mu.Lock()

	oldMap := make(map[string]bool)
	for _, cam := range rm.cameras {
		if cam.Enabled {
			oldMap[cam.StreamKey] = true
		}
	}

	newMap := make(map[string]CameraInfo)
	for _, cam := range newCameras {
		if cam.Enabled {
			newMap[cam.StreamKey] = cam
		}
	}

	// Stop removed cameras
	type stoppedRec struct {
		key   string
		state *recorderState
	}
	var toStop []stoppedRec
	for key := range oldMap {
		if _, exists := newMap[key]; !exists {
			log.Printf("[reload] Stopping recorder for %s (removed)", key)
			if state, ok := rm.states[key]; ok {
				close(state.stopCh)
				delete(rm.states, key)
				toStop = append(toStop, stoppedRec{key, state})
			}
		}
	}

	// Prepare new cameras for start
	var toStart []CameraInfo
	for key, cam := range newMap {
		if !oldMap[key] {
			log.Printf("[reload] Starting recorder for %s", key)
			toStart = append(toStart, cam)
		}
	}

	rm.cameras = newCameras
	rm.mu.Unlock()

	// Kill stopped FFmpeg processes
	for _, s := range toStop {
		if s.state.cmd != nil && s.state.cmd.Process != nil {
			s.state.cmd.Process.Signal(syscall.SIGTERM)
			time.Sleep(3 * time.Second)
			s.state.cmd.Process.Kill()
		}
	}

	// Start new recorders
	for _, cam := range toStart {
		rm.startRecorder(cam)
	}

	log.Printf("[reload] Reconciled: %d cameras recording", len(newMap))
}

// Stop terminates all recording processes.
func (rm *RecordingManager) Stop() {
	rm.mu.Lock()
	type activeProc struct {
		key  string
		proc *os.Process
	}
	var procs []activeProc
	for key, state := range rm.states {
		close(state.stopCh)
		if state.cmd != nil && state.cmd.Process != nil {
			state.cmd.Process.Signal(syscall.SIGTERM)
			procs = append(procs, activeProc{key, state.cmd.Process})
		}
	}
	rm.mu.Unlock()

	if len(procs) == 0 {
		return
	}

	time.Sleep(5 * time.Second)
	for _, p := range procs {
		if err := p.proc.Kill(); err == nil {
			log.Printf("[%s] FFmpeg did not exit after 5s SIGTERM, sent SIGKILL", p.key)
		}
	}
}

// ProtectSegment marks a file path as protected from cleanup.
func (rm *RecordingManager) ProtectSegment(path string) {
	rm.protectedMu.Lock()
	rm.protected[path] = true
	rm.protectedMu.Unlock()
}

// UnprotectSegment removes protection from a file path.
func (rm *RecordingManager) UnprotectSegment(path string) {
	rm.protectedMu.Lock()
	delete(rm.protected, path)
	rm.protectedMu.Unlock()
}

// IsProtected checks if a file path is protected.
func (rm *RecordingManager) IsProtected(path string) bool {
	rm.protectedMu.RLock()
	defer rm.protectedMu.RUnlock()
	return rm.protected[path]
}

// CleanupOldSegments deletes .ts files older than the rolling window.
func (rm *RecordingManager) CleanupOldSegments(rollingWindow time.Duration) {
	entries, err := os.ReadDir(rm.recordingsDir)
	if err != nil {
		log.Printf("[cleanup] Failed to read recordings dir: %v", err)
		return
	}

	cutoff := time.Now().Add(-rollingWindow)
	deleted := 0

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		streamDir := filepath.Join(rm.recordingsDir, entry.Name())
		files, err := os.ReadDir(streamDir)
		if err != nil {
			continue
		}
		for _, f := range files {
			if f.IsDir() || !strings.HasSuffix(f.Name(), ".ts") {
				continue
			}
			fullPath := filepath.Join(streamDir, f.Name())

			if rm.IsProtected(fullPath) {
				continue
			}

			// Parse timestamp from filename: YYYYMMDD_HHMMSS.ts
			name := strings.TrimSuffix(f.Name(), ".ts")
			segTime, err := time.Parse("20060102_150405", name)
			if err != nil {
				// Can't parse timestamp — use file mod time
				info, err := f.Info()
				if err != nil {
					continue
				}
				segTime = info.ModTime()
			}

			if segTime.Before(cutoff) {
				if err := os.Remove(fullPath); err == nil {
					deleted++
				}
			}
		}
	}

	if deleted > 0 {
		log.Printf("[cleanup] Deleted %d old segment(s)", deleted)
	}
}

func main() {
	rtmpBaseURL := os.Getenv("STREAMING_RTMP_URL")
	if rtmpBaseURL == "" {
		rtmpBaseURL = "rtmp://streaming:1935/live"
	}

	recordingsDir := os.Getenv("RECORDINGS_DIR")
	if recordingsDir == "" {
		recordingsDir = "/recordings"
	}

	webBackendURL := os.Getenv("WEB_BACKEND_URL")
	if webBackendURL == "" {
		webBackendURL = "http://web-backend:8080"
	}

	rollingMinutes := 60
	if env := os.Getenv("ROLLING_WINDOW_MINUTES"); env != "" {
		if v, err := strconv.Atoi(env); err == nil && v > 0 {
			rollingMinutes = v
		}
	}
	rollingWindow := time.Duration(rollingMinutes) * time.Minute

	ffmpegTimeout := 60 * time.Second
	if env := os.Getenv("FFMPEG_TIMEOUT"); env != "" {
		if secs, err := strconv.Atoi(env); err == nil && secs > 0 {
			ffmpegTimeout = time.Duration(secs) * time.Second
		}
	}

	log.Printf("Recording service starting (rolling window: %dm, ffmpeg timeout: %v)", rollingMinutes, ffmpegTimeout)

	manager := NewRecordingManager(rtmpBaseURL, recordingsDir, ffmpegTimeout)

	// Fetch initial camera list from web-backend
	reloadClient := &http.Client{Timeout: 10 * time.Second}
	cameras := fetchCameras(reloadClient, webBackendURL)
	if len(cameras) > 0 {
		log.Printf("Starting recording for %d camera(s)", len(cameras))
		manager.Start(cameras)
	} else {
		log.Println("No cameras found. Waiting for reload.")
	}

	// Start cleanup goroutine
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			manager.CleanupOldSegments(rollingWindow)
		}
	}()

	// HTTP server
	mux := http.NewServeMux()

	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"ok","service":"recording"}`))
	})

	mux.HandleFunc("GET /api/status", func(w http.ResponseWriter, r *http.Request) {
		statuses := manager.GetStatuses()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(statuses)
	})

	mux.HandleFunc("POST /api/cameras/reload", func(w http.ResponseWriter, r *http.Request) {
		log.Println("[reload] Reload triggered, fetching cameras from web-backend")

		cameras := fetchCameras(reloadClient, webBackendURL)
		manager.Reload(cameras)

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"status":  "reloaded",
			"cameras": len(cameras),
		})
	})

	log.Println("recording service listening on :8080")
	if err := http.ListenAndServe(":8080", mux); err != nil {
		manager.Stop()
		log.Fatal(err)
	}
}

// fetchCameras retrieves the camera list from web-backend.
func fetchCameras(client *http.Client, webBackendURL string) []CameraInfo {
	resp, err := client.Get(webBackendURL + "/internal/cameras")
	if err != nil {
		log.Printf("[fetch] Failed to fetch cameras: %v", err)
		return nil
	}
	defer resp.Body.Close()

	var raw []struct {
		Name       string `json:"name"`
		StreamKey  string `json:"streamKey"`
		SourceType string `json:"sourceType"`
		SourceURL  string `json:"sourceUrl"`
		Enabled    bool   `json:"enabled"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		log.Printf("[fetch] Failed to decode cameras: %v", err)
		return nil
	}

	var cameras []CameraInfo
	for _, c := range raw {
		if c.Enabled && c.StreamKey != "" {
			cameras = append(cameras, CameraInfo{
				StreamKey: c.StreamKey,
				Name:      c.Name,
				Enabled:   true,
			})
		}
	}

	log.Printf("[fetch] Found %d enabled camera(s)", len(cameras))
	return cameras
}
