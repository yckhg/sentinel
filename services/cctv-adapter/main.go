package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

// CameraConfig represents a single camera from the configuration file.
type CameraConfig struct {
	CameraID string `json:"cameraId"`
	Name     string `json:"name"`
	RtspURL  string `json:"rtspUrl"`
}

// CameraStatus tracks the runtime state of a camera connection.
type CameraStatus struct {
	CameraID    string  `json:"cameraId"`
	Status      string  `json:"status"` // connected, disconnected, reconnecting
	ConnectedAt *string `json:"connectedAt"`
	LastError   *string `json:"lastError"`
}

// watchdogWriter wraps an io.Writer and tracks the last time data was written.
type watchdogWriter struct {
	inner    io.Writer
	lastSeen atomic.Int64 // unix timestamp of last write
}

func newWatchdogWriter(inner io.Writer) *watchdogWriter {
	w := &watchdogWriter{inner: inner}
	w.lastSeen.Store(time.Now().Unix())
	return w
}

func (w *watchdogWriter) Write(p []byte) (int, error) {
	w.lastSeen.Store(time.Now().Unix())
	return w.inner.Write(p)
}

// CameraManager manages FFmpeg processes for all cameras.
type CameraManager struct {
	mu        sync.RWMutex
	cameras   []CameraConfig
	statuses  map[string]*cameraState
	streamURL string
	timeout   time.Duration

	// newCmd builds the ffmpeg command for a camera. It is a field so tests can
	// substitute a stand-in binary (e.g. sleep) and exercise the lifecycle/race
	// paths without a real ffmpeg.
	newCmd func(cam CameraConfig, destURL string) *exec.Cmd
}

type cameraState struct {
	status      string
	connectedAt *time.Time
	lastError   *string
	cmd         *exec.Cmd
	stopCh      chan struct{}
}

func NewCameraManager(cameras []CameraConfig, streamURL string, timeout time.Duration) *CameraManager {
	return &CameraManager{
		cameras:   cameras,
		statuses:  make(map[string]*cameraState),
		streamURL: streamURL,
		timeout:   timeout,
		newCmd:    defaultFFmpegCmd,
	}
}

// defaultFFmpegCmd builds the real ffmpeg pull-RTSP / push-RTMP command.
// -c copy ensures no transcoding (raw H.264 pass-through).
func defaultFFmpegCmd(cam CameraConfig, destURL string) *exec.Cmd {
	return exec.Command("ffmpeg",
		"-hide_banner",
		"-loglevel", "warning",
		"-rtsp_transport", "tcp",
		"-i", cam.RtspURL,
		"-c", "copy",
		"-f", "flv",
		"-flvflags", "no_duration_filesize",
		destURL,
	)
}

// Start launches FFmpeg processes for all configured cameras.
func (cm *CameraManager) Start() {
	for _, cam := range cm.cameras {
		state := &cameraState{
			status: "disconnected",
			stopCh: make(chan struct{}),
		}
		cm.mu.Lock()
		cm.statuses[cam.CameraID] = state
		cm.mu.Unlock()

		go cm.manageCameraStream(cam, state)
	}
}

// manageCameraStream runs the FFmpeg process for a camera with auto-reconnect.
func (cm *CameraManager) manageCameraStream(cam CameraConfig, state *cameraState) {
	backoff := time.Second
	maxBackoff := 30 * time.Second

	for {
		select {
		case <-state.stopCh:
			return
		default:
		}

		cm.mu.Lock()
		state.status = "reconnecting"
		cm.mu.Unlock()

		log.Printf("[%s] Connecting to RTSP: %s", cam.CameraID, cam.RtspURL)

		// Build FFmpeg command: pull RTSP, push RTMP to streaming server.
		destURL := fmt.Sprintf("%s/%s", cm.streamURL, cam.CameraID)
		cmd := cm.newCmd(cam, destURL)
		// Wrap stdout/stderr with watchdog writers to detect hung FFmpeg
		stdoutWatcher := newWatchdogWriter(os.Stdout)
		stderrWatcher := newWatchdogWriter(os.Stderr)
		cmd.Stdout = stdoutWatcher
		cmd.Stderr = stderrWatcher

		// Re-check stopCh right before starting: Reload/Stop close stopCh and kill
		// the process they snapshotted, but a goroutine sitting between the loop
		// top and here would otherwise spawn a fresh ffmpeg nobody tracked. Bail
		// out before starting if a stop was requested. (#66)
		select {
		case <-state.stopCh:
			return
		default:
		}

		cm.mu.Lock()
		state.cmd = cmd
		cm.mu.Unlock()

		err := cmd.Start()
		if err != nil {
			errMsg := fmt.Sprintf("Failed to start FFmpeg: %v", err)
			log.Printf("[%s] %s", cam.CameraID, errMsg)
			cm.mu.Lock()
			state.status = "disconnected"
			state.lastError = &errMsg
			state.connectedAt = nil
			cm.mu.Unlock()

			time.Sleep(backoff)
			backoff = min(backoff*2, maxBackoff)
			continue
		}

		// Mark as connected once process starts successfully
		now := time.Now().UTC()
		cm.mu.Lock()
		state.status = "connected"
		state.connectedAt = &now
		state.lastError = nil
		cm.mu.Unlock()

		log.Printf("[%s] Connected, streaming to %s", cam.CameraID, destURL)
		backoff = time.Second // reset backoff on successful connect

		// Start watchdog goroutine to kill FFmpeg if no output for timeout duration.
		// It also owns terminating *this* goroutine's process when a stop is
		// requested (stopCh), so an ffmpeg started after Reload/Stop snapshotted
		// the process set can never survive as an orphan. (#66)
		processDone := make(chan struct{})
		go func() {
			ticker := time.NewTicker(cm.timeout / 2)
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
					if time.Since(lastOutput) > cm.timeout {
						log.Printf("[%s] FFmpeg output timeout (%v since last output), stopping process gracefully", cam.CameraID, cm.timeout)
						terminateProcess(cam.CameraID, cmd.Process, processDone)
						return
					}
				case <-processDone:
					return
				case <-state.stopCh:
					// Stop/Reload requested: terminate our own process so it cannot
					// leak as an orphan (the loop will then observe stopCh and exit).
					terminateProcess(cam.CameraID, cmd.Process, processDone)
					return
				}
			}
		}()

		// Wait for process to exit
		err = cmd.Wait()
		close(processDone)

		if err != nil {
			errMsg := fmt.Sprintf("FFmpeg exited: %v", err)
			log.Printf("[%s] %s", cam.CameraID, errMsg)
			cm.mu.Lock()
			state.status = "disconnected"
			state.lastError = &errMsg
			state.connectedAt = nil
			cm.mu.Unlock()
		} else {
			log.Printf("[%s] FFmpeg exited cleanly", cam.CameraID)
			cm.mu.Lock()
			state.status = "disconnected"
			state.connectedAt = nil
			cm.mu.Unlock()
		}

		// Wait before reconnecting
		select {
		case <-state.stopCh:
			return
		case <-time.After(backoff):
		}
		backoff = min(backoff*2, maxBackoff)
	}
}

// GetStatuses returns the current status of all cameras.
func (cm *CameraManager) GetStatuses() []CameraStatus {
	cm.mu.RLock()
	defer cm.mu.RUnlock()

	result := make([]CameraStatus, 0, len(cm.cameras))
	for _, cam := range cm.cameras {
		state, ok := cm.statuses[cam.CameraID]
		if !ok {
			result = append(result, CameraStatus{
				CameraID: cam.CameraID,
				Status:   "disconnected",
			})
			continue
		}

		cs := CameraStatus{
			CameraID:  cam.CameraID,
			Status:    state.status,
			LastError: state.lastError,
		}
		if state.connectedAt != nil {
			t := state.connectedAt.Format(time.RFC3339)
			cs.ConnectedAt = &t
		}
		result = append(result, cs)
	}
	return result
}

// Reload reconciles the running cameras with a new config list.
// Unchanged cameras (same cameraID + rtspUrl) are not interrupted.
// Removed cameras are stopped. New/changed cameras are (re)started.
// Uses a single lock scope to prevent race conditions between reading
// old state and writing new state.
func (cm *CameraManager) Reload(newCameras []CameraConfig) {
	cm.mu.Lock()

	// Build old state map
	oldMap := make(map[string]string) // cameraID -> rtspUrl
	for _, cam := range cm.cameras {
		oldMap[cam.CameraID] = cam.RtspURL
	}

	newMap := make(map[string]CameraConfig)
	for _, cam := range newCameras {
		newMap[cam.CameraID] = cam
	}

	// Collect cameras to stop (removed or changed). Snapshot the *os.Process
	// while still holding the lock — reading state.cmd after Unlock would race
	// with manageCameraStream writing state.cmd under the same lock. (#66)
	type stoppedCamera struct {
		id   string
		proc *os.Process
	}
	var toStop []stoppedCamera
	for id, oldURL := range oldMap {
		newCam, exists := newMap[id]
		if !exists || newCam.RtspURL != oldURL {
			log.Printf("[reload] Stopping camera %s (removed or changed)", id)
			if state, ok := cm.statuses[id]; ok {
				close(state.stopCh)
				var proc *os.Process
				if state.cmd != nil {
					proc = state.cmd.Process
				}
				delete(cm.statuses, id)
				toStop = append(toStop, stoppedCamera{id, proc})
			}
		}
	}

	// Prepare new/changed cameras for start
	var toStart []CameraConfig
	for id, newCam := range newMap {
		oldURL, existed := oldMap[id]
		if !existed || oldURL != newCam.RtspURL {
			log.Printf("[reload] Starting camera %s", id)
			toStart = append(toStart, newCam)
			state := &cameraState{
				status: "disconnected",
				stopCh: make(chan struct{}),
			}
			cm.statuses[newCam.CameraID] = state
		}
	}

	// Update the cameras list while still holding the lock
	cm.cameras = newCameras

	// Snapshot new states to start goroutines after unlock
	startStates := make(map[string]*cameraState, len(toStart))
	for _, cam := range toStart {
		startStates[cam.CameraID] = cm.statuses[cam.CameraID]
	}

	cm.mu.Unlock()

	// Terminate removed FFmpeg processes in parallel outside the lock: SIGTERM
	// every process, wait a single grace period, then SIGKILL any survivors.
	// Previously this slept 3s per process serially, so removing N cameras
	// blocked the reload handler for N×3s and could race reload retries into
	// duplicate reloads. This mirrors Stop()'s one-shot grace. (#44)
	var stopProcs []*os.Process
	for _, s := range toStop {
		if s.proc != nil {
			log.Printf("[%s] Sending SIGTERM to FFmpeg process", s.id)
			stopProcs = append(stopProcs, s.proc)
		}
	}
	terminateProcesses(stopProcs, 3*time.Second)

	// Launch goroutines for new cameras
	for _, cam := range toStart {
		go cm.manageCameraStream(cam, startStates[cam.CameraID])
	}

	log.Printf("[reload] Reconciled: %d cameras active", len(newCameras))
}

// terminateProcess sends SIGTERM to a single process and waits up to 5s for it
// to exit (observed via processDone), then SIGKILLs. Used by the per-stream
// watchdog so a goroutine always kills its own ffmpeg on stop. (#66)
func terminateProcess(id string, proc *os.Process, processDone <-chan struct{}) {
	if proc == nil {
		return
	}
	proc.Signal(syscall.SIGTERM)
	select {
	case <-time.After(5 * time.Second):
		log.Printf("[%s] FFmpeg did not exit after 5s SIGTERM, sending SIGKILL", id)
		proc.Kill()
	case <-processDone:
		// Process exited gracefully after SIGTERM.
	}
}

// terminateProcesses sends SIGTERM to every process, waits a single grace
// period, then SIGKILLs any that remain. The grace wait runs once for the whole
// batch (not per process), so teardown time does not scale with the process
// count. Kill on an already-exited process is harmless.
func terminateProcesses(procs []*os.Process, grace time.Duration) {
	if len(procs) == 0 {
		return
	}
	for _, p := range procs {
		p.Signal(syscall.SIGTERM)
	}
	time.Sleep(grace)
	for _, p := range procs {
		p.Kill()
	}
}

// Stop terminates all FFmpeg processes gracefully (SIGTERM → 5s wait → SIGKILL).
func (cm *CameraManager) Stop() {
	cm.mu.Lock()
	type activeProc struct {
		id   string
		proc *os.Process
	}
	var procs []activeProc
	for id, state := range cm.statuses {
		close(state.stopCh)
		if state.cmd != nil && state.cmd.Process != nil {
			log.Printf("[%s] Sending SIGTERM to FFmpeg process", id)
			state.cmd.Process.Signal(syscall.SIGTERM)
			procs = append(procs, activeProc{id, state.cmd.Process})
		}
	}
	cm.mu.Unlock()

	if len(procs) == 0 {
		return
	}

	// Wait up to 5s for graceful exit, then SIGKILL remaining
	time.Sleep(5 * time.Second)
	for _, p := range procs {
		// Kill is harmless if process already exited
		if err := p.proc.Kill(); err == nil {
			log.Printf("[%s] FFmpeg did not exit after 5s SIGTERM, sent SIGKILL", p.id)
		}
	}
}

func loadCamerasConfig(path string) ([]CameraConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading camera config: %w", err)
	}

	var cameras []CameraConfig
	if err := json.Unmarshal(data, &cameras); err != nil {
		return nil, fmt.Errorf("parsing camera config: %w", err)
	}

	return cameras, nil
}

func main() {
	// Load camera configuration
	configPath := os.Getenv("CAMERAS_CONFIG_PATH")
	if configPath == "" {
		configPath = "/config/cameras.json"
	}

	streamURL := os.Getenv("STREAMING_RTMP_URL")
	if streamURL == "" {
		streamURL = "rtmp://streaming:1935/live"
	}

	cameras, err := loadCamerasConfig(configPath)
	if err != nil {
		log.Printf("Warning: Could not load camera config from %s: %v", configPath, err)
		log.Println("Starting with no cameras configured. Update config and restart to add cameras.")
		cameras = []CameraConfig{}
	}

	// Parse FFmpeg output timeout (kills hung processes)
	ffmpegTimeout := 30 * time.Second
	if envTimeout := os.Getenv("FFMPEG_TIMEOUT"); envTimeout != "" {
		if secs, err := strconv.Atoi(envTimeout); err == nil && secs > 0 {
			ffmpegTimeout = time.Duration(secs) * time.Second
		} else {
			log.Printf("Warning: Invalid FFMPEG_TIMEOUT '%s', using default %v", envTimeout, ffmpegTimeout)
		}
	}

	log.Printf("Loaded %d camera(s) from config (ffmpeg timeout: %v)", len(cameras), ffmpegTimeout)
	for _, cam := range cameras {
		log.Printf("  Camera: %s (%s)", cam.CameraID, cam.Name)
	}

	// Create camera manager and start streaming
	manager := NewCameraManager(cameras, streamURL, ffmpegTimeout)
	if len(cameras) > 0 {
		manager.Start()
	}

	// HTTP server
	mux := http.NewServeMux()

	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"ok","service":"cctv-adapter"}`))
	})

	mux.HandleFunc("GET /api/cameras/status", func(w http.ResponseWriter, r *http.Request) {
		statuses := manager.GetStatuses()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(statuses)
	})

	// Reload endpoint: fetches camera list from web-backend and reconciles
	webBackendURL := os.Getenv("WEB_BACKEND_URL")
	if webBackendURL == "" {
		webBackendURL = "http://web-backend:8080"
	}
	reloadClient := &http.Client{Timeout: 10 * time.Second}

	mux.HandleFunc("POST /api/cameras/reload", func(w http.ResponseWriter, r *http.Request) {
		log.Println("[reload] Reload triggered, fetching cameras from web-backend")

		resp, err := reloadClient.Get(webBackendURL + "/internal/cameras")
		if err != nil {
			log.Printf("[reload] Failed to fetch cameras: %v", err)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadGateway)
			w.Write([]byte(`{"error":"failed to fetch cameras from web-backend"}`))
			return
		}
		defer resp.Body.Close()

		// A non-200 response is a degraded/failed web-backend reply, not an
		// authoritative "empty camera list". Treat it as an access failure and
		// preserve the currently running push processes (spec §output: reload
		// failure → 502, existing processes unchanged). Only an explicit 200
		// with a valid body is allowed to reconcile/teardown.
		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
			log.Printf("[reload] web-backend returned non-200 status %d: %s", resp.StatusCode, string(body))
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadGateway)
			w.Write([]byte(`{"error":"web-backend returned non-200 status"}`))
			return
		}

		var cameras []struct {
			Name       string `json:"name"`
			StreamKey  string `json:"streamKey"`
			SourceType string `json:"sourceType"`
			SourceURL  string `json:"sourceUrl"`
			Enabled    bool   `json:"enabled"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&cameras); err != nil {
			log.Printf("[reload] Failed to decode cameras: %v", err)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte(`{"error":"failed to decode cameras response"}`))
			return
		}

		// Filter to enabled RTSP cameras only and convert to CameraConfig
		var rtspCameras []CameraConfig
		for _, c := range cameras {
			if c.SourceType == "rtsp" && c.Enabled && c.SourceURL != "" && c.StreamKey != "" {
				rtspCameras = append(rtspCameras, CameraConfig{
					CameraID: c.StreamKey,
					Name:     c.Name,
					RtspURL:  c.SourceURL,
				})
			}
		}

		manager.Reload(rtspCameras)

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]any{
			"status":  "reloaded",
			"cameras": len(rtspCameras),
		})
	})

	// Graceful shutdown: on SIGTERM/SIGINT, terminate ffmpeg children through
	// the SIGTERM → 5s grace → SIGKILL path (manager.Stop blocks until done)
	// before exiting, so streaming observes a clean teardown rather than an
	// abrupt namespace kill of the child processes.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		<-sigCh
		log.Println("Shutting down, stopping camera streams...")
		manager.Stop()
		os.Exit(0)
	}()

	srv := newHTTPServer(mux)

	log.Println("cctv-adapter listening on :8080")
	if err := srv.ListenAndServe(); err != nil {
		manager.Stop()
		log.Fatal(err)
	}
}

// newHTTPServer builds the service HTTP server with hardened timeouts. Without
// them ReadHeaderTimeout/ReadTimeout/IdleTimeout default to 0 (unlimited) and a
// slow/malicious client can trickle headers or body to hold goroutines/sockets
// open indefinitely (Slowloris).
func newHTTPServer(handler http.Handler) *http.Server {
	return &http.Server{
		Addr:              ":8080",
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
}
