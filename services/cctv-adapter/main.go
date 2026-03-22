package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"sync"
	"sync/atomic"
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
	}
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

		// Build FFmpeg command: pull RTSP, push RTMP to streaming server
		// -c copy ensures no transcoding (raw H.264 pass-through)
		destURL := fmt.Sprintf("%s/%s", cm.streamURL, cam.CameraID)
		cmd := exec.Command("ffmpeg",
			"-hide_banner",
			"-loglevel", "warning",
			"-rtsp_transport", "tcp",
			"-i", cam.RtspURL,
			"-c", "copy",
			"-f", "flv",
			"-flvflags", "no_duration_filesize",
			destURL,
		)
		// Wrap stdout/stderr with watchdog writers to detect hung FFmpeg
		stdoutWatcher := newWatchdogWriter(os.Stdout)
		stderrWatcher := newWatchdogWriter(os.Stderr)
		cmd.Stdout = stdoutWatcher
		cmd.Stderr = stderrWatcher

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

		// Start watchdog goroutine to kill FFmpeg if no output for timeout duration
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
						log.Printf("[%s] FFmpeg output timeout (%v since last output), killing process", cam.CameraID, cm.timeout)
						if cmd.Process != nil {
							cmd.Process.Kill()
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

// Stop terminates all FFmpeg processes.
func (cm *CameraManager) Stop() {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	for id, state := range cm.statuses {
		close(state.stopCh)
		if state.cmd != nil && state.cmd.Process != nil {
			log.Printf("[%s] Stopping FFmpeg process", id)
			state.cmd.Process.Kill()
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

	log.Println("cctv-adapter listening on :8080")
	if err := http.ListenAndServe(":8080", mux); err != nil {
		manager.Stop()
		log.Fatal(err)
	}
}
