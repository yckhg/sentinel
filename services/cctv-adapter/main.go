package main

import (
	"bufio"
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

// progressLiveness tracks the last time an ffmpeg `-progress` update was
// observed. ffmpeg emits key=value progress lines (frame=, fps=, out_time=,
// progress=continue, ...) to its `-progress` fd roughly twice a second while it
// is actively moving frames. The watchdog treats the *arrival of these lines* —
// not general log output — as the liveness signal, so a healthy but quiet
// stream (`-loglevel warning` + `-c copy`, which is silent on stderr) is not
// mistaken for a hang and restarted every FFMPEG_TIMEOUT. (#68)
type progressLiveness struct {
	lastSeen atomic.Int64 // unix timestamp of last progress line observed
}

func newProgressLiveness() *progressLiveness {
	p := &progressLiveness{}
	p.lastSeen.Store(time.Now().Unix())
	return p
}

func (p *progressLiveness) mark(t time.Time) {
	p.lastSeen.Store(t.Unix())
}

func (p *progressLiveness) last() time.Time {
	return time.Unix(p.lastSeen.Load(), 0)
}

// consumeProgress reads ffmpeg `-progress` output line by line and marks
// liveness (via now()) on every non-empty line. Any progress line is a
// sufficient liveness signal. It returns when the reader reaches EOF, which for
// the process pipe happens when ffmpeg exits and every write end is closed.
func consumeProgress(r io.Reader, live *progressLiveness, now func() time.Time) {
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		if len(scanner.Bytes()) == 0 {
			continue
		}
		live.mark(now())
	}
}

// isStalled reports whether the elapsed time since the last liveness signal has
// exceeded timeout. A frozen/SIGSTOPped ffmpeg stops emitting progress, so this
// still detects genuine hangs (spec §J); a healthy stream keeps emitting
// progress and is never flagged.
func isStalled(last, now time.Time, timeout time.Duration) bool {
	return now.Sub(last) > timeout
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
		// Emit machine-readable progress (frame=, out_time=, progress=continue, ...)
		// to fd 3 ~2×/sec. The watchdog uses the arrival of these updates as the
		// liveness signal, so a healthy but log-silent stream is not mistaken for a
		// hang. fd 3 is wired to a pipe via cmd.ExtraFiles in manageCameraStream. (#68)
		"-progress", "pipe:3",
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
		// Logs flow to the container's stdout/stderr unchanged. Liveness is NOT
		// derived from log output: with `-loglevel warning` + `-c copy` a healthy
		// stream is silent, so treating "no log output" as a hang restarted a
		// healthy process every FFMPEG_TIMEOUT (#68). Instead the watchdog reads
		// ffmpeg's `-progress` stream on fd 3 (see below).
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr

		// Wire a pipe to the child's fd 3 for `-progress pipe:3`. ExtraFiles[0]
		// becomes fd 3 in the child. A reader goroutine (started after Start)
		// marks liveness on each progress update.
		live := newProgressLiveness()
		var progressR, progressW *os.File
		if pr, pw, perr := os.Pipe(); perr == nil {
			progressR, progressW = pr, pw
			cmd.ExtraFiles = append(cmd.ExtraFiles, progressW)
		} else {
			// fd exhaustion is the only realistic cause. Run without a progress
			// pipe this cycle; liveness stays at the process start time, so a
			// truly silent cycle could still trip the watchdog — acceptable for
			// this rare degraded case.
			log.Printf("[%s] progress pipe unavailable (%v); liveness falls back to start time", cam.CameraID, perr)
		}

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
			// Start failed: no child inherited the pipe, so close both ends here
			// to avoid leaking fds across reconnect attempts.
			if progressW != nil {
				progressW.Close()
			}
			if progressR != nil {
				progressR.Close()
			}
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

		// The child now holds its own dup of the progress write end. Close the
		// parent's copy so the reader observes EOF when ffmpeg exits, and start
		// the reader that converts progress updates into liveness marks. (#68)
		if progressR != nil {
			progressW.Close()
			go func(r *os.File) {
				defer r.Close()
				consumeProgress(r, live, time.Now)
			}(progressR)
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
					// Liveness = time since the last `-progress` update on fd 3.
					// A frozen/SIGSTOPped ffmpeg stops emitting progress, so this
					// still detects genuine hangs (spec §J); a healthy silent
					// stream keeps emitting progress and is left alone (#68).
					//
					// Threshold note (#68 decision 2): progress arrives ~2×/sec, so
					// FFMPEG_TIMEOUT (default 30s) is deliberately conservative
					// slack — far larger than progress-based liveness needs. It is
					// kept unchanged to avoid regressing the tuned watchdog window.
					if isStalled(live.last(), time.Now(), cm.timeout) {
						log.Printf("[%s] FFmpeg progress stalled (%v since last progress update), stopping process gracefully", cam.CameraID, cm.timeout)
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
