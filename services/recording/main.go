package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
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
		// -fflags +genpts+discardcorrupt: regenerate PTS and discard corrupt frames
		// -avoid_negative_ts make_zero: shift timestamps to start from zero
		// -c copy: no transcoding (H.264 passthrough)
		// -f segment: output as individual segment files
		// -segment_time 10: 10-second segments
		// -strftime 1: use timestamp-based filenames
		// -segment_atclocktime 1: align segments to clock time
		// -reset_timestamps 1: reset timestamps per segment for clean playback
		cmd := exec.Command("ffmpeg",
			"-hide_banner",
			"-loglevel", "warning",
			"-fflags", "+genpts+discardcorrupt",
			"-i", srcURL,
			"-avoid_negative_ts", "make_zero",
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

// TimeRange represents a contiguous range of available recording segments.
type TimeRange struct {
	Start string `json:"start"` // ISO8601
	End   string `json:"end"`   // ISO8601
}

// ListSegments returns available time ranges for a given stream key.
// Adjacent segments (within 15s gap tolerance) are merged into contiguous ranges.
func (rm *RecordingManager) ListSegments(streamKey string) ([]TimeRange, error) {
	streamDir := filepath.Join(rm.recordingsDir, streamKey)
	files, err := os.ReadDir(streamDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	// Parse all .ts segment timestamps and sort them
	var segTimes []time.Time
	for _, f := range files {
		if f.IsDir() || !strings.HasSuffix(f.Name(), ".ts") {
			continue
		}
		name := strings.TrimSuffix(f.Name(), ".ts")
		t, err := time.Parse("20060102_150405", name)
		if err != nil {
			continue
		}
		segTimes = append(segTimes, t)
	}

	if len(segTimes) == 0 {
		return nil, nil
	}

	sort.Slice(segTimes, func(i, j int) bool { return segTimes[i].Before(segTimes[j]) })

	// Merge adjacent segments (gap <= 15s = segment_time + tolerance)
	const segDuration = 10 * time.Second
	const gapTolerance = 15 * time.Second

	var ranges []TimeRange
	rangeStart := segTimes[0]
	rangeEnd := segTimes[0].Add(segDuration)

	for i := 1; i < len(segTimes); i++ {
		if segTimes[i].Sub(rangeEnd) <= gapTolerance {
			rangeEnd = segTimes[i].Add(segDuration)
		} else {
			ranges = append(ranges, TimeRange{
				Start: rangeStart.UTC().Format(time.RFC3339),
				End:   rangeEnd.UTC().Format(time.RFC3339),
			})
			rangeStart = segTimes[i]
			rangeEnd = segTimes[i].Add(segDuration)
		}
	}
	ranges = append(ranges, TimeRange{
		Start: rangeStart.UTC().Format(time.RFC3339),
		End:   rangeEnd.UTC().Format(time.RFC3339),
	})

	return ranges, nil
}

// GeneratePlaylist creates an HLS playlist (.m3u8) for segments within a time range.
func (rm *RecordingManager) GeneratePlaylist(streamKey string, from, to time.Time) (string, error) {
	streamDir := filepath.Join(rm.recordingsDir, streamKey)
	files, err := os.ReadDir(streamDir)
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("no recordings found")
		}
		return "", err
	}

	const segDuration = 10 // seconds

	type segment struct {
		t    time.Time
		name string
	}

	var segments []segment
	for _, f := range files {
		if f.IsDir() || !strings.HasSuffix(f.Name(), ".ts") {
			continue
		}
		name := strings.TrimSuffix(f.Name(), ".ts")
		t, err := time.Parse("20060102_150405", name)
		if err != nil {
			continue
		}
		segEnd := t.Add(segDuration * time.Second)
		// Include segment if it overlaps with [from, to]
		if segEnd.After(from) && t.Before(to) {
			segments = append(segments, segment{t: t, name: f.Name()})
		}
	}

	if len(segments) == 0 {
		return "", fmt.Errorf("no segments in requested range")
	}

	sort.Slice(segments, func(i, j int) bool { return segments[i].t.Before(segments[j].t) })

	// Build M3U8 playlist
	var b strings.Builder
	b.WriteString("#EXTM3U\n")
	b.WriteString("#EXT-X-VERSION:3\n")
	b.WriteString(fmt.Sprintf("#EXT-X-TARGETDURATION:%d\n", segDuration))
	b.WriteString("#EXT-X-MEDIA-SEQUENCE:0\n")
	b.WriteString("#EXT-X-PLAYLIST-TYPE:VOD\n")

	for _, seg := range segments {
		b.WriteString(fmt.Sprintf("#EXTINF:%d.0,\n", segDuration))
		b.WriteString(fmt.Sprintf("/api/recordings/%s/segments/%s\n", streamKey, seg.name))
	}
	b.WriteString("#EXT-X-ENDLIST\n")

	return b.String(), nil
}

// --- Archive Management ---

// ArchiveMetadata stores information about an archived clip.
type ArchiveMetadata struct {
	ID           string `json:"id"`
	IncidentID   string `json:"incidentId"`
	StreamKey    string `json:"streamKey"`
	From         string `json:"from"`
	To           string `json:"to"`
	CreatedAt    string `json:"createdAt"`
	SizeBytes    int64  `json:"sizeBytes"`
	FilePath     string `json:"filePath"`
	Status       string `json:"status"` // protecting, pending, processing, completed, failed
	Error        string `json:"error,omitempty"`
	IncidentTime string `json:"incidentTime,omitempty"` // original incident timestamp for auto-finalize
}

// ArchiveManager manages archive creation and metadata.
type ArchiveManager struct {
	mu            sync.RWMutex
	archives      []ArchiveMetadata
	archivesDir   string
	recordingsDir string
	recManager    *RecordingManager
	metadataPath  string
}

func NewArchiveManager(archivesDir, recordingsDir string, recManager *RecordingManager) *ArchiveManager {
	am := &ArchiveManager{
		archivesDir:   archivesDir,
		recordingsDir: recordingsDir,
		recManager:    recManager,
		metadataPath:  filepath.Join(archivesDir, "metadata.json"),
	}
	am.loadMetadata()
	return am
}

func (am *ArchiveManager) loadMetadata() {
	data, err := os.ReadFile(am.metadataPath)
	if err != nil {
		return
	}
	var archives []ArchiveMetadata
	if err := json.Unmarshal(data, &archives); err != nil {
		log.Printf("[archive] Failed to load metadata: %v", err)
		return
	}
	am.archives = archives
	log.Printf("[archive] Loaded %d archive(s) from metadata", len(archives))
}

func (am *ArchiveManager) saveMetadata() {
	data, err := json.MarshalIndent(am.archives, "", "  ")
	if err != nil {
		log.Printf("[archive] Failed to marshal metadata: %v", err)
		return
	}
	if err := os.WriteFile(am.metadataPath, data, 0644); err != nil {
		log.Printf("[archive] Failed to save metadata: %v", err)
	}
}

// ListArchives returns all archive metadata.
func (am *ArchiveManager) ListArchives() []ArchiveMetadata {
	am.mu.RLock()
	defer am.mu.RUnlock()
	result := make([]ArchiveMetadata, len(am.archives))
	copy(result, am.archives)
	return result
}

// CreateArchive creates an archive for the given parameters.
// It protects segments, merges them into MP4, and stores metadata.
func (am *ArchiveManager) CreateArchive(incidentID, streamKey string, from, to time.Time) (*ArchiveMetadata, error) {
	archiveID := fmt.Sprintf("%s_%s_%s", incidentID, streamKey, from.UTC().Format("20060102_150405"))

	// Check for duplicate
	am.mu.RLock()
	for _, a := range am.archives {
		if a.ID == archiveID {
			am.mu.RUnlock()
			return &a, nil // Already exists
		}
	}
	am.mu.RUnlock()

	meta := ArchiveMetadata{
		ID:         archiveID,
		IncidentID: incidentID,
		StreamKey:  streamKey,
		From:       from.UTC().Format(time.RFC3339),
		To:         to.UTC().Format(time.RFC3339),
		CreatedAt:  time.Now().UTC().Format(time.RFC3339),
		Status:     "pending",
	}

	am.mu.Lock()
	am.archives = append(am.archives, meta)
	am.saveMetadata()
	am.mu.Unlock()

	// Run archive creation in background
	go am.processArchive(archiveID, streamKey, from, to)

	return &meta, nil
}

func (am *ArchiveManager) processArchive(archiveID, streamKey string, from, to time.Time) {
	am.updateStatus(archiveID, "processing", "")

	streamDir := filepath.Join(am.recordingsDir, streamKey)
	files, err := os.ReadDir(streamDir)
	if err != nil {
		am.updateStatus(archiveID, "failed", fmt.Sprintf("read dir: %v", err))
		return
	}

	const segDuration = 10 * time.Second

	// Collect segments in the time range
	type segment struct {
		t    time.Time
		path string
	}
	var segments []segment
	for _, f := range files {
		if f.IsDir() || !strings.HasSuffix(f.Name(), ".ts") {
			continue
		}
		name := strings.TrimSuffix(f.Name(), ".ts")
		t, err := time.Parse("20060102_150405", name)
		if err != nil {
			continue
		}
		segEnd := t.Add(segDuration)
		if segEnd.After(from) && t.Before(to) {
			fullPath := filepath.Join(streamDir, f.Name())
			segments = append(segments, segment{t: t, path: fullPath})
		}
	}

	if len(segments) == 0 {
		am.updateStatus(archiveID, "failed", "no segments in requested range")
		return
	}

	sort.Slice(segments, func(i, j int) bool { return segments[i].t.Before(segments[j].t) })

	// Protect all segments from cleanup
	for _, seg := range segments {
		am.recManager.ProtectSegment(seg.path)
	}

	// Create archive output directory
	outDir := filepath.Join(am.archivesDir, archiveID)
	os.MkdirAll(outDir, 0755)
	outFile := filepath.Join(outDir, streamKey+".mp4")

	// Create concat list file for FFmpeg
	concatFile := filepath.Join(outDir, "concat.txt")
	var concatContent strings.Builder
	for _, seg := range segments {
		concatContent.WriteString(fmt.Sprintf("file '%s'\n", seg.path))
	}
	if err := os.WriteFile(concatFile, []byte(concatContent.String()), 0644); err != nil {
		am.updateStatus(archiveID, "failed", fmt.Sprintf("write concat file: %v", err))
		return
	}

	// Merge segments into MP4 using FFmpeg (copy, no transcoding)
	cmd := exec.Command("ffmpeg",
		"-hide_banner",
		"-loglevel", "warning",
		"-f", "concat",
		"-safe", "0",
		"-i", concatFile,
		"-c", "copy",
		"-movflags", "+faststart",
		outFile,
	)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	log.Printf("[archive] Merging %d segments into %s", len(segments), outFile)
	if err := cmd.Run(); err != nil {
		am.updateStatus(archiveID, "failed", fmt.Sprintf("ffmpeg merge: %v", err))
		return
	}

	// Clean up concat file
	os.Remove(concatFile)

	// Get file size
	info, err := os.Stat(outFile)
	if err != nil {
		am.updateStatus(archiveID, "failed", fmt.Sprintf("stat output: %v", err))
		return
	}

	am.mu.Lock()
	for i, a := range am.archives {
		if a.ID == archiveID {
			am.archives[i].Status = "completed"
			am.archives[i].SizeBytes = info.Size()
			am.archives[i].FilePath = outFile
			break
		}
	}
	am.saveMetadata()
	am.mu.Unlock()

	log.Printf("[archive] Completed: %s (%d bytes, %d segments)", archiveID, info.Size(), len(segments))
}

func (am *ArchiveManager) updateStatus(archiveID, status, errMsg string) {
	am.mu.Lock()
	defer am.mu.Unlock()
	for i, a := range am.archives {
		if a.ID == archiveID {
			am.archives[i].Status = status
			am.archives[i].Error = errMsg
			break
		}
	}
	am.saveMetadata()
	if errMsg != "" {
		log.Printf("[archive] %s: status=%s error=%s", archiveID, status, errMsg)
	}
}

// DeleteArchive removes an archive and unprotects its segments.
func (am *ArchiveManager) DeleteArchive(archiveID string) error {
	am.mu.Lock()
	defer am.mu.Unlock()

	idx := -1
	for i, a := range am.archives {
		if a.ID == archiveID {
			idx = i
			break
		}
	}
	if idx == -1 {
		return fmt.Errorf("archive not found: %s", archiveID)
	}

	archive := am.archives[idx]

	// Remove the archive directory
	archiveDir := filepath.Join(am.archivesDir, archiveID)
	os.RemoveAll(archiveDir)

	// Unprotect segments that were in this archive's range
	if archive.StreamKey != "" {
		fromTime, _ := time.Parse(time.RFC3339, archive.From)
		toTime, _ := time.Parse(time.RFC3339, archive.To)
		if !fromTime.IsZero() && !toTime.IsZero() {
			am.unprotectSegments(archive.StreamKey, fromTime, toTime)
		}
	}

	// Remove from list
	am.archives = append(am.archives[:idx], am.archives[idx+1:]...)
	am.saveMetadata()

	log.Printf("[archive] Deleted: %s", archiveID)
	return nil
}

// DeleteIncidentArchives removes ALL archives for a given incident ID.
func (am *ArchiveManager) DeleteIncidentArchives(incidentID string) (int, error) {
	am.mu.Lock()
	defer am.mu.Unlock()

	var toDelete []int
	for i, a := range am.archives {
		if a.IncidentID == incidentID {
			toDelete = append(toDelete, i)
		}
	}

	if len(toDelete) == 0 {
		return 0, fmt.Errorf("no archives found for incident: %s", incidentID)
	}

	// Delete in reverse order to maintain indices
	for i := len(toDelete) - 1; i >= 0; i-- {
		idx := toDelete[i]
		archive := am.archives[idx]

		// Remove the archive directory
		archiveDir := filepath.Join(am.archivesDir, archive.ID)
		os.RemoveAll(archiveDir)

		// Unprotect segments
		if archive.StreamKey != "" {
			fromTime, _ := time.Parse(time.RFC3339, archive.From)
			toTime, _ := time.Parse(time.RFC3339, archive.To)
			if !fromTime.IsZero() && !toTime.IsZero() {
				am.unprotectSegments(archive.StreamKey, fromTime, toTime)
			}
		}

		// Remove from list
		am.archives = append(am.archives[:idx], am.archives[idx+1:]...)
	}

	am.saveMetadata()
	log.Printf("[archive] Deleted %d archive(s) for incident %s", len(toDelete), incidentID)
	return len(toDelete), nil
}

// ProtectIncidentSegments marks segments from (incidentTime - 1h) to now as protected for all stream keys.
// Creates archive entries with status "protecting" — segments won't be cleaned up until finalized.
func (am *ArchiveManager) ProtectIncidentSegments(incidentID string, streamKeys []string, incidentTime time.Time) []ArchiveMetadata {
	protectFrom := incidentTime.Add(-1 * time.Hour)
	now := time.Now().UTC()

	var created []ArchiveMetadata
	for _, streamKey := range streamKeys {
		archiveID := fmt.Sprintf("%s_%s_%s", incidentID, streamKey, protectFrom.UTC().Format("20060102_150405"))

		// Check for duplicate
		am.mu.RLock()
		exists := false
		for _, a := range am.archives {
			if a.ID == archiveID {
				exists = true
				created = append(created, a)
				break
			}
		}
		am.mu.RUnlock()
		if exists {
			continue
		}

		meta := ArchiveMetadata{
			ID:           archiveID,
			IncidentID:   incidentID,
			StreamKey:    streamKey,
			From:         protectFrom.UTC().Format(time.RFC3339),
			To:           now.Format(time.RFC3339), // placeholder, will be updated on finalize
			CreatedAt:    now.Format(time.RFC3339),
			Status:       "protecting",
			IncidentTime: incidentTime.UTC().Format(time.RFC3339),
		}

		am.mu.Lock()
		am.archives = append(am.archives, meta)
		am.saveMetadata()
		am.mu.Unlock()

		// Protect existing segments from cleanup
		am.protectSegmentsInRange(streamKey, protectFrom, now)

		created = append(created, meta)
		log.Printf("[archive] Protecting segments for %s/%s from %s", incidentID, streamKey, protectFrom.Format(time.RFC3339))
	}

	return created
}

// protectSegmentsInRange marks all segments in the given time range as protected.
func (am *ArchiveManager) protectSegmentsInRange(streamKey string, from, to time.Time) {
	streamDir := filepath.Join(am.recordingsDir, streamKey)
	files, err := os.ReadDir(streamDir)
	if err != nil {
		return
	}
	const segDuration = 10 * time.Second
	for _, f := range files {
		if f.IsDir() || !strings.HasSuffix(f.Name(), ".ts") {
			continue
		}
		name := strings.TrimSuffix(f.Name(), ".ts")
		t, err := time.Parse("20060102_150405", name)
		if err != nil {
			continue
		}
		segEnd := t.Add(segDuration)
		if segEnd.After(from) && t.Before(to) {
			fullPath := filepath.Join(streamDir, f.Name())
			am.recManager.ProtectSegment(fullPath)
		}
	}
}

// RefreshProtection re-protects segments for all "protecting" archives.
// Called periodically so new segments written after the initial protect call are also protected.
func (am *ArchiveManager) RefreshProtection() {
	am.mu.RLock()
	var protecting []ArchiveMetadata
	for _, a := range am.archives {
		if a.Status == "protecting" {
			protecting = append(protecting, a)
		}
	}
	am.mu.RUnlock()

	for _, a := range protecting {
		fromTime, _ := time.Parse(time.RFC3339, a.From)
		if fromTime.IsZero() {
			continue
		}
		// Protect from original start up to now (segments keep arriving)
		am.protectSegmentsInRange(a.StreamKey, fromTime, time.Now().UTC())
	}
}

// FinalizeIncidentArchives finalizes all "protecting" archives for an incident.
// Merges segments from original From to resolvedAt + 30min into MP4.
func (am *ArchiveManager) FinalizeIncidentArchives(incidentID string, resolvedAt time.Time) (int, error) {
	am.mu.RLock()
	var toFinalize []ArchiveMetadata
	for _, a := range am.archives {
		if a.IncidentID == incidentID && a.Status == "protecting" {
			toFinalize = append(toFinalize, a)
		}
	}
	am.mu.RUnlock()

	if len(toFinalize) == 0 {
		return 0, fmt.Errorf("no protecting archives found for incident: %s", incidentID)
	}

	finalizeTo := resolvedAt.Add(30 * time.Minute)

	for _, a := range toFinalize {
		fromTime, _ := time.Parse(time.RFC3339, a.From)
		if fromTime.IsZero() {
			continue
		}

		// Update the To time and status
		am.mu.Lock()
		for i, ar := range am.archives {
			if ar.ID == a.ID {
				am.archives[i].To = finalizeTo.UTC().Format(time.RFC3339)
				am.archives[i].Status = "finalizing"
				break
			}
		}
		am.saveMetadata()
		am.mu.Unlock()

		// Process archive in background (same as CreateArchive but with updated range)
		go am.processArchive(a.ID, a.StreamKey, fromTime, finalizeTo)
	}

	log.Printf("[archive] Finalizing %d archive(s) for incident %s (to=%s)", len(toFinalize), incidentID, finalizeTo.Format(time.RFC3339))
	return len(toFinalize), nil
}

// AutoFinalizeExpired checks for "protecting" archives older than maxAge and auto-finalizes them.
func (am *ArchiveManager) AutoFinalizeExpired(maxAge time.Duration) {
	am.mu.RLock()
	var expired []ArchiveMetadata
	now := time.Now().UTC()
	for _, a := range am.archives {
		if a.Status != "protecting" {
			continue
		}
		incidentTime, err := time.Parse(time.RFC3339, a.IncidentTime)
		if err != nil {
			continue
		}
		if now.Sub(incidentTime) > maxAge {
			expired = append(expired, a)
		}
	}
	am.mu.RUnlock()

	// Group by incidentID to finalize once per incident
	seen := map[string]bool{}
	for _, a := range expired {
		if seen[a.IncidentID] {
			continue
		}
		seen[a.IncidentID] = true
		log.Printf("[archive] Auto-finalizing expired incident %s (age > %v)", a.IncidentID, maxAge)
		am.FinalizeIncidentArchives(a.IncidentID, now)
	}
}

func (am *ArchiveManager) unprotectSegments(streamKey string, from, to time.Time) {
	streamDir := filepath.Join(am.recordingsDir, streamKey)
	files, _ := os.ReadDir(streamDir)
	const segDuration = 10 * time.Second

	for _, f := range files {
		if f.IsDir() || !strings.HasSuffix(f.Name(), ".ts") {
			continue
		}
		name := strings.TrimSuffix(f.Name(), ".ts")
		t, err := time.Parse("20060102_150405", name)
		if err != nil {
			continue
		}
		segEnd := t.Add(segDuration)
		if segEnd.After(from) && t.Before(to) {
			fullPath := filepath.Join(streamDir, f.Name())
			// Only unprotect if no other archive references this segment
			if !am.isSegmentInOtherArchive(fullPath, "") {
				am.recManager.UnprotectSegment(fullPath)
			}
		}
	}
}

func (am *ArchiveManager) isSegmentInOtherArchive(segPath, excludeArchiveID string) bool {
	for _, a := range am.archives {
		if a.ID == excludeArchiveID || a.Status == "failed" {
			continue
		}
		fromTime, _ := time.Parse(time.RFC3339, a.From)
		toTime, _ := time.Parse(time.RFC3339, a.To)
		if fromTime.IsZero() || toTime.IsZero() {
			continue
		}
		// Check if this segment's time falls within archive range
		dir := filepath.Dir(segPath)
		streamKey := filepath.Base(dir)
		if streamKey != a.StreamKey {
			continue
		}
		name := strings.TrimSuffix(filepath.Base(segPath), ".ts")
		t, err := time.Parse("20060102_150405", name)
		if err != nil {
			continue
		}
		segEnd := t.Add(10 * time.Second)
		if segEnd.After(fromTime) && t.Before(toTime) {
			return true
		}
	}
	return false
}

// GetStorageStats returns disk usage info for recordings and archives, plus filesystem stats.
func (am *ArchiveManager) GetStorageStats() map[string]any {
	recordingsSize := dirSize(am.recordingsDir)
	archivesSize := dirSize(am.archivesDir)

	stats := map[string]any{
		"recordingsBytes": recordingsSize,
		"archivesBytes":   archivesSize,
		"totalUsedBytes":  recordingsSize + archivesSize,
		"archiveCount":    len(am.archives),
	}

	// Get filesystem-level disk stats
	var stat syscall.Statfs_t
	if err := syscall.Statfs(am.recordingsDir, &stat); err == nil {
		totalDisk := stat.Blocks * uint64(stat.Bsize)
		freeDisk := stat.Bavail * uint64(stat.Bsize)
		usedDisk := totalDisk - (stat.Bfree * uint64(stat.Bsize))
		stats["diskTotalBytes"] = totalDisk
		stats["diskUsedBytes"] = usedDisk
		stats["diskAvailableBytes"] = freeDisk
	}

	return stats
}

func dirSize(path string) int64 {
	var size int64
	filepath.Walk(path, func(_ string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		size += info.Size()
		return nil
	})
	return size
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

	archivesDir := os.Getenv("ARCHIVES_DIR")
	if archivesDir == "" {
		archivesDir = "/archives"
	}

	log.Printf("Recording service starting (rolling window: %dm, ffmpeg timeout: %v)", rollingMinutes, ffmpegTimeout)

	manager := NewRecordingManager(rtmpBaseURL, recordingsDir, ffmpegTimeout)
	archiveManager := NewArchiveManager(archivesDir, recordingsDir, manager)

	// Fetch initial camera list from web-backend (retry on failure)
	reloadClient := &http.Client{Timeout: 10 * time.Second}
	var cameras []CameraInfo
	for attempt := 1; attempt <= 10; attempt++ {
		cameras = fetchCameras(reloadClient, webBackendURL)
		if len(cameras) > 0 {
			break
		}
		log.Printf("No cameras found (attempt %d/10), retrying in 3s...", attempt)
		time.Sleep(3 * time.Second)
	}
	if len(cameras) > 0 {
		log.Printf("Starting recording for %d camera(s)", len(cameras))
		manager.Start(cameras)
	} else {
		log.Println("No cameras found after retries. Waiting for reload.")
	}

	// Start cleanup goroutine
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			manager.CleanupOldSegments(rollingWindow)
		}
	}()

	// Start protection refresh + auto-finalize goroutine
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			// Re-protect new segments for active incidents
			archiveManager.RefreshProtection()
			// Auto-finalize incidents protecting for > 2 hours
			archiveManager.AutoFinalizeExpired(2 * time.Hour)
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

	// GET /api/recordings/{stream_key} — list available time ranges
	mux.HandleFunc("GET /api/recordings/{stream_key}", func(w http.ResponseWriter, r *http.Request) {
		streamKey := r.PathValue("stream_key")
		if streamKey == "" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]string{"error": "stream_key is required"})
			return
		}

		ranges, err := manager.ListSegments(streamKey)
		if err != nil {
			log.Printf("[recordings] list segments error for %s: %v", streamKey, err)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]string{"error": "internal error"})
			return
		}

		if len(ranges) == 0 {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusNotFound)
			json.NewEncoder(w).Encode(map[string]string{"error": "no recordings found"})
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"streamKey":  streamKey,
			"timeRanges": ranges,
		})
	})

	// GET /api/recordings/{stream_key}/play?from=ISO8601&to=ISO8601 — HLS playlist
	mux.HandleFunc("GET /api/recordings/{stream_key}/play", func(w http.ResponseWriter, r *http.Request) {
		streamKey := r.PathValue("stream_key")
		if streamKey == "" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]string{"error": "stream_key is required"})
			return
		}

		fromStr := r.URL.Query().Get("from")
		toStr := r.URL.Query().Get("to")
		if fromStr == "" || toStr == "" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]string{"error": "from and to query parameters are required (ISO8601)"})
			return
		}

		from, err := time.Parse(time.RFC3339, fromStr)
		if err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]string{"error": "invalid from parameter: must be ISO8601/RFC3339"})
			return
		}
		to, err := time.Parse(time.RFC3339, toStr)
		if err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]string{"error": "invalid to parameter: must be ISO8601/RFC3339"})
			return
		}

		playlist, err := manager.GeneratePlaylist(streamKey, from, to)
		if err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusNotFound)
			json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}

		w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
		w.Header().Set("Cache-Control", "no-cache")
		w.Write([]byte(playlist))
	})

	// GET /api/recordings/{stream_key}/segments/{filename} — serve .ts segment file
	mux.HandleFunc("GET /api/recordings/{stream_key}/segments/{filename}", func(w http.ResponseWriter, r *http.Request) {
		streamKey := r.PathValue("stream_key")
		filename := r.PathValue("filename")

		// Validate filename to prevent path traversal
		if strings.Contains(filename, "/") || strings.Contains(filename, "\\") || strings.Contains(filename, "..") {
			http.Error(w, "invalid filename", http.StatusBadRequest)
			return
		}
		if !strings.HasSuffix(filename, ".ts") {
			http.Error(w, "invalid file type", http.StatusBadRequest)
			return
		}

		filePath := filepath.Join(recordingsDir, streamKey, filename)
		w.Header().Set("Content-Type", "video/mp2t")
		w.Header().Set("Cache-Control", "public, max-age=3600")
		http.ServeFile(w, r, filePath)
	})

	// POST /api/archives — create an archive
	mux.HandleFunc("POST /api/archives", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			IncidentID string   `json:"incidentId"`
			StreamKeys []string `json:"streamKeys"`
			From       string   `json:"from"`
			To         string   `json:"to"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]string{"error": "invalid JSON"})
			return
		}

		if len(req.StreamKeys) == 0 {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]string{"error": "streamKeys is required"})
			return
		}

		from, err := time.Parse(time.RFC3339, req.From)
		if err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]string{"error": "invalid from: must be ISO8601/RFC3339"})
			return
		}
		to, err := time.Parse(time.RFC3339, req.To)
		if err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]string{"error": "invalid to: must be ISO8601/RFC3339"})
			return
		}

		incidentID := req.IncidentID
		if incidentID == "" {
			incidentID = fmt.Sprintf("manual_%s", time.Now().UTC().Format("20060102_150405"))
		}

		var created []ArchiveMetadata
		for _, streamKey := range req.StreamKeys {
			meta, err := archiveManager.CreateArchive(incidentID, streamKey, from, to)
			if err != nil {
				log.Printf("[archive] Failed to create archive for %s: %v", streamKey, err)
				continue
			}
			created = append(created, *meta)
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		json.NewEncoder(w).Encode(map[string]any{
			"status":   "accepted",
			"archives": created,
		})
	})

	// POST /api/archives/protect — Phase 1: protect segments for an incident
	mux.HandleFunc("POST /api/archives/protect", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			IncidentID   string   `json:"incidentId"`
			StreamKeys   []string `json:"streamKeys"`
			IncidentTime string   `json:"incidentTime"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]string{"error": "invalid JSON"})
			return
		}
		if req.IncidentID == "" || len(req.StreamKeys) == 0 || req.IncidentTime == "" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]string{"error": "incidentId, streamKeys, and incidentTime are required"})
			return
		}

		incidentTime, err := time.Parse(time.RFC3339, req.IncidentTime)
		if err != nil {
			incidentTime, err = time.Parse("2006-01-02 15:04:05", req.IncidentTime)
			if err != nil {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusBadRequest)
				json.NewEncoder(w).Encode(map[string]string{"error": "invalid incidentTime format"})
				return
			}
		}

		created := archiveManager.ProtectIncidentSegments(req.IncidentID, req.StreamKeys, incidentTime)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		json.NewEncoder(w).Encode(map[string]any{
			"status":   "protecting",
			"archives": created,
		})
	})

	// POST /api/archives/finalize — Phase 2: finalize archives for an incident
	mux.HandleFunc("POST /api/archives/finalize", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			IncidentID string `json:"incidentId"`
			ResolvedAt string `json:"resolvedAt"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]string{"error": "invalid JSON"})
			return
		}
		if req.IncidentID == "" || req.ResolvedAt == "" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]string{"error": "incidentId and resolvedAt are required"})
			return
		}

		resolvedAt, err := time.Parse(time.RFC3339, req.ResolvedAt)
		if err != nil {
			resolvedAt, err = time.Parse("2006-01-02 15:04:05", req.ResolvedAt)
			if err != nil {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusBadRequest)
				json.NewEncoder(w).Encode(map[string]string{"error": "invalid resolvedAt format"})
				return
			}
		}

		count, err := archiveManager.FinalizeIncidentArchives(req.IncidentID, resolvedAt)
		if err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusNotFound)
			json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"status": "finalizing",
			"count":  count,
		})
	})

	// GET /api/archives — list all archives
	mux.HandleFunc("GET /api/archives", func(w http.ResponseWriter, r *http.Request) {
		archives := archiveManager.ListArchives()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(archives)
	})

	// DELETE /api/archives/{id} — delete an archive
	mux.HandleFunc("DELETE /api/archives/{id}", func(w http.ResponseWriter, r *http.Request) {
		archiveID := r.PathValue("id")
		if archiveID == "" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]string{"error": "archive id is required"})
			return
		}

		if err := archiveManager.DeleteArchive(archiveID); err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusNotFound)
			json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": "deleted"})
	})

	// DELETE /api/archives/incident/{incidentId} — delete all archives for an incident
	mux.HandleFunc("DELETE /api/archives/incident/{incidentId}", func(w http.ResponseWriter, r *http.Request) {
		incidentID := r.PathValue("incidentId")
		if incidentID == "" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]string{"error": "incidentId is required"})
			return
		}

		count, err := archiveManager.DeleteIncidentArchives(incidentID)
		if err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusNotFound)
			json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"status": "deleted", "count": count})
	})

	// GET /api/archives/{id}/download — serve archive MP4 file
	mux.HandleFunc("GET /api/archives/{id}/download", func(w http.ResponseWriter, r *http.Request) {
		archiveID := r.PathValue("id")
		archives := archiveManager.ListArchives()
		for _, a := range archives {
			if a.ID == archiveID {
				if a.Status != "completed" {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusConflict)
					json.NewEncoder(w).Encode(map[string]string{"error": "archive not ready", "status": a.Status})
					return
				}
				w.Header().Set("Content-Type", "video/mp4")
				w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"%s.mp4\"", archiveID))
				http.ServeFile(w, r, a.FilePath)
				return
			}
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]string{"error": "archive not found"})
	})

	// GET /api/storage — disk usage stats
	mux.HandleFunc("GET /api/storage", func(w http.ResponseWriter, r *http.Request) {
		stats := archiveManager.GetStorageStats()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(stats)
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
