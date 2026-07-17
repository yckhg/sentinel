package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

// pathComponentRe matches a safe single path segment used to build filesystem
// paths (stream keys, incident IDs). It permits only alphanumerics, '_' and '-',
// which structurally excludes path separators and ".." traversal. (#73)
var pathComponentRe = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// isValidPathComponent reports whether s is safe to use as a path segment.
func isValidPathComponent(s string) bool {
	return pathComponentRe.MatchString(s)
}

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

// progressLiveness tracks the last time an ffmpeg `-progress` update was
// observed. ffmpeg emits key=value progress lines (frame=, fps=, out_time=,
// progress=continue, ...) to its `-progress` fd roughly twice a second while it
// is actively moving frames. The watchdog treats the *arrival of these lines* —
// not general log output — as the liveness signal, so a healthy but quiet
// recorder (`-loglevel warning` + `-c copy`, which is silent on stderr) is not
// mistaken for a hang and restarted every FFMPEG_TIMEOUT, which would gap the
// continuous recording and undermine evidence-video reliability. (#68 pattern)
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
// still detects genuine hangs; a healthy recorder keeps emitting progress and is
// never flagged.
func isStalled(last, now time.Time, timeout time.Duration) bool {
	return now.Sub(last) > timeout
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

		cmd := buildRecordCmd(srcURL, segPattern)

		// Logs flow to the container's stdout/stderr unchanged. Liveness is NOT
		// derived from log output: with `-loglevel warning` + `-c copy` a healthy
		// recorder is silent, so treating "no log output" as a hang restarted a
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
			log.Printf("[%s] progress pipe unavailable (%v); liveness falls back to start time", cam.StreamKey, perr)
		}

		rm.mu.Lock()
		state.cmd = cmd
		rm.mu.Unlock()

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
					// Liveness = time since the last `-progress` update on fd 3.
					// A frozen/SIGSTOPped ffmpeg stops emitting progress, so this
					// still detects genuine hangs; a healthy silent recorder keeps
					// emitting progress and is left alone (#68).
					//
					// Threshold note: progress arrives ~2×/sec, so FFMPEG_TIMEOUT
					// (default 60s for recording; cctv-adapter uses 30s) is
					// deliberately conservative slack — far larger than
					// progress-based liveness needs. It is kept unchanged to avoid
					// regressing the tuned watchdog window.
					if isStalled(live.last(), time.Now(), rm.timeout) {
						log.Printf("[%s] FFmpeg progress stalled (%v since last progress update), stopping", cam.StreamKey, rm.timeout)
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

// buildRecordCmd builds the segmenting ffmpeg command for a recorder.
//
// ffmpeg -strftime uses the process's *local* time to name segment files, but
// the whole service parses those names with time.Parse (which yields UTC) and
// compares them against UTC query ranges / rolling cutoffs. If the container TZ
// were not UTC, filenames and queries would drift by the offset and break
// playback/archive/cleanup. Force TZ=UTC on the child so filenames are always
// UTC wall-clock regardless of the host/container timezone. (#77)
//
//   - -fflags +genpts+discardcorrupt: regenerate PTS, discard corrupt frames
//   - -avoid_negative_ts make_zero:   shift timestamps to start from zero
//   - -c copy:                        no transcoding (H.264 passthrough)
//   - -f segment / -segment_time 10:  10-second segment files
//   - -strftime 1 / -segment_atclocktime 1: clock-aligned timestamp filenames
//   - -reset_timestamps 1:            reset per segment for clean playback
func buildRecordCmd(srcURL, segPattern string) *exec.Cmd {
	cmd := exec.Command("ffmpeg",
		"-hide_banner",
		"-loglevel", "warning",
		// Emit machine-readable progress (frame=, out_time=, progress=continue, ...)
		// to fd 3 ~2×/sec. The watchdog uses the arrival of these updates as the
		// liveness signal, so a healthy but log-silent recorder is not mistaken for
		// a hang. fd 3 is wired to a pipe via cmd.ExtraFiles in manageRecording. (#68)
		"-progress", "pipe:3",
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
	cmd.Env = append(os.Environ(), "TZ=UTC")
	return cmd
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

	// Terminate removed FFmpeg processes in parallel: SIGTERM every process,
	// wait a single grace period, then SIGKILL any survivors. Previously this
	// slept 3s per process serially, so removing N recorders blocked the reload
	// handler for N×3s and could race reload retries into duplicate reloads.
	// This mirrors Stop()'s one-shot grace. (#44)
	var stopProcs []*os.Process
	for _, s := range toStop {
		if s.state.cmd != nil && s.state.cmd.Process != nil {
			stopProcs = append(stopProcs, s.state.cmd.Process)
		}
	}
	terminateProcesses(stopProcs, 3*time.Second)

	// Start new recorders
	for _, cam := range toStart {
		rm.startRecorder(cam)
	}

	log.Printf("[reload] Reconciled: %d cameras recording", len(newMap))
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

// zeroByteGrace is how long a 0-byte segment must be untouched before cleanup
// will reap it, protecting freshly-created segments still being written. (#80)
var zeroByteGrace = 60 * time.Second

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

			// A segment ffmpeg just created can momentarily be 0 bytes before the
			// first flush. Only reap 0-byte files once they have been stale for a
			// grace period, so an actively-writing segment is never deleted out
			// from under the recorder. (#80)
			info, infoErr := f.Info()
			if infoErr == nil && info.Size() == 0 {
				if time.Since(info.ModTime()) > zeroByteGrace {
					if err := os.Remove(fullPath); err == nil {
						log.Printf("[cleanup] Deleted stale empty segment: %s", f.Name())
						deleted++
					}
				}
				continue
			}

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

	const segDuration = 10 * time.Second

	var segments []playlistSeg
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
		// Include segment if it overlaps with [from, to]
		if segEnd.After(from) && t.Before(to) {
			segments = append(segments, playlistSeg{t: t, name: f.Name()})
		}
	}

	if len(segments) == 0 {
		return "", fmt.Errorf("no segments in requested range")
	}

	sort.Slice(segments, func(i, j int) bool { return segments[i].t.Before(segments[j].t) })

	return buildPlaylist(streamKey, segments), nil
}

type playlistSeg struct {
	t    time.Time
	name string
}

// buildPlaylist renders a VOD M3U8 from time-sorted segments. Instead of a flat
// #EXTINF:10.0 for every entry, it derives each segment's real duration from the
// gap to the next segment (segments are clock-aligned, so this equals the wall
// time actually covered), and inserts #EXT-X-DISCONTINUITY where a gap exceeds
// the tolerance. This avoids timeline drift over long ranges and decoder
// artifacts at gap boundaries. (#78)
func buildPlaylist(streamKey string, segs []playlistSeg) string {
	const nominal = 10.0 // seconds; target/last-segment fallback length
	const gapTolerance = 15 * time.Second

	durs := make([]float64, len(segs))
	maxDur := nominal
	for i := range segs {
		dur := nominal
		if i < len(segs)-1 {
			diff := segs[i+1].t.Sub(segs[i].t)
			// A diff within tolerance is the segment's real covered duration
			// (including minor drift). A larger diff is a gap: the true length is
			// unknown, so fall back to nominal and emit a discontinuity below.
			if diff > 0 && diff <= gapTolerance {
				dur = diff.Seconds()
			}
		}
		durs[i] = dur
		if dur > maxDur {
			maxDur = dur
		}
	}

	var b strings.Builder
	b.WriteString("#EXTM3U\n")
	b.WriteString("#EXT-X-VERSION:3\n")
	b.WriteString(fmt.Sprintf("#EXT-X-TARGETDURATION:%d\n", int(math.Ceil(maxDur))))
	b.WriteString("#EXT-X-MEDIA-SEQUENCE:0\n")
	b.WriteString("#EXT-X-PLAYLIST-TYPE:VOD\n")

	for i, seg := range segs {
		if i > 0 && segs[i].t.Sub(segs[i-1].t) > gapTolerance {
			b.WriteString("#EXT-X-DISCONTINUITY\n")
		}
		b.WriteString(fmt.Sprintf("#EXTINF:%.3f,\n", durs[i]))
		b.WriteString(fmt.Sprintf("/api/recordings/%s/segments/%s\n", streamKey, seg.name))
	}
	b.WriteString("#EXT-X-ENDLIST\n")

	return b.String()
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
	Status       string `json:"status"` // enum SSOT (spec): protecting, pending, finalizing, processing, completed, failed
	// CompletedAt is the RFC3339 (UTC) instant the archive reached `completed`.
	// It is recorded ATOMICALLY with the completed transition (see markCompleted)
	// and is non-null ONLY when Status == "completed"; null/absent otherwise, so a
	// consumer never observes a completed archive without its ready timestamp
	// (archive-download-ux 단위A 핵심로직 "동시적 불변식", 단언 A3). (#archive-download-ux)
	CompletedAt  *string `json:"completedAt,omitempty"`
	// Error carries the human-readable failure reason. Its wire/JSON key is
	// `lastError` (spec unifies the prior "error"/"reason" naming to recording's
	// lastError); non-empty for every `failed` archive (단언 A4). (#archive-download-ux)
	Error        string `json:"lastError,omitempty"`
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

	// TTL caches for the expensive directory-size walks behind /api/storage. (#81)
	recSizeCache *sizeCache
	arcSizeCache *sizeCache

	// Archive retention policy thresholds (archive-retention-policy.md). Both are
	// opt-in: <=0 disables that policy. Wired from ARCHIVE_MAX_BYTES /
	// ARCHIVE_RETENTION_DAYS in main() and consumed by EvictArchives.
	evictMaxBytes      int64
	evictRetentionDays int

	// Retention diagnostic dedup state (touched only from the single ticker
	// goroutine via EvictArchives). They suppress per-cycle log spam: a warning is
	// re-emitted only when the underlying condition CHANGES, not every 30s while a
	// persistent floor/unparseable condition stays the same.
	lastFloorWarnedID   string          // last floor-preserved archive id warned ("" = none)
	warnedUnparsableIDs map[string]bool // set of unparseable-CreatedAt ids already warned
}

// storageCacheTTL bounds how stale /api/storage size figures may be.
const storageCacheTTL = 15 * time.Second

// sizeCache memoizes a directory-size computation for a TTL so a polling UI does
// not trigger a full filepath.Walk on every /api/storage request. (#81)
type sizeCache struct {
	mu  sync.Mutex
	ttl time.Duration
	val int64
	at  time.Time
	now func() time.Time
}

func newSizeCache(ttl time.Duration) *sizeCache {
	return &sizeCache{ttl: ttl, now: time.Now}
}

// get returns the cached value if it is younger than the TTL, otherwise it
// recomputes via compute and refreshes the cache.
func (c *sizeCache) get(compute func() int64) int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.at.IsZero() && c.now().Sub(c.at) < c.ttl {
		return c.val
	}
	c.val = compute()
	c.at = c.now()
	return c.val
}

func NewArchiveManager(archivesDir, recordingsDir string, recManager *RecordingManager) *ArchiveManager {
	am := &ArchiveManager{
		archivesDir:   archivesDir,
		recordingsDir: recordingsDir,
		recManager:    recManager,
		metadataPath:  filepath.Join(archivesDir, "metadata.json"),
		recSizeCache:  newSizeCache(storageCacheTTL),
		arcSizeCache:  newSizeCache(storageCacheTTL),
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
		// Corrupt/truncated metadata (e.g. from a crash mid-write on the old
		// non-atomic path). Preserve the bad file for forensics instead of
		// silently overwriting it on the next save, and alert loudly. (#74)
		backup := fmt.Sprintf("%s.corrupt.%d", am.metadataPath, time.Now().Unix())
		if rerr := os.Rename(am.metadataPath, backup); rerr != nil {
			log.Printf("[archive] ALERT: metadata unreadable (%v) and backup failed: %v", err, rerr)
		} else {
			log.Printf("[archive] ALERT: metadata unreadable (%v); moved corrupt file to %s", err, backup)
		}
		return
	}
	am.archives = archives
	log.Printf("[archive] Loaded %d archive(s) from metadata", len(archives))
}

// saveMetadata writes metadata atomically: marshal to a temp file in the same
// directory, then rename over the target. A crash mid-write can now only leave a
// stale-but-valid metadata.json (or an orphan .tmp), never a truncated file that
// fails to unmarshal and loses the entire archive list. (#74)
func (am *ArchiveManager) saveMetadata() {
	data, err := json.MarshalIndent(am.archives, "", "  ")
	if err != nil {
		log.Printf("[archive] Failed to marshal metadata: %v", err)
		return
	}
	tmp := am.metadataPath + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		log.Printf("[archive] Failed to write metadata temp file: %v", err)
		return
	}
	if err := os.Rename(tmp, am.metadataPath); err != nil {
		log.Printf("[archive] Failed to rename metadata into place: %v", err)
		os.Remove(tmp)
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
	// Reject traversal-bearing identifiers before they are joined into paths. (#73)
	if !isValidPathComponent(incidentID) || !isValidPathComponent(streamKey) {
		return nil, fmt.Errorf("invalid incidentId or streamKey")
	}

	archiveID := fmt.Sprintf("%s_%s_%s", incidentID, streamKey, from.UTC().Format("20060102_150405"))

	meta := ArchiveMetadata{
		ID:         archiveID,
		IncidentID: incidentID,
		StreamKey:  streamKey,
		From:       from.UTC().Format(time.RFC3339),
		To:         to.UTC().Format(time.RFC3339),
		CreatedAt:  time.Now().UTC().Format(time.RFC3339),
		Status:     "pending",
	}

	// Duplicate check and append must happen under a single write lock: two
	// concurrent requests for the same incident/stream/time both passing an
	// RLock check and then appending would insert the same archiveID twice. (#79)
	am.mu.Lock()
	for _, a := range am.archives {
		if a.ID == archiveID {
			existing := a
			am.mu.Unlock()
			return &existing, nil // Already exists
		}
	}
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

	// Protect all segments from cleanup during the merge.
	for _, seg := range segments {
		am.recManager.ProtectSegment(seg.path)
	}

	// Release the originals once the merge attempt finishes: on success they are
	// captured in the MP4; on failure keeping them pinned forever would defeat
	// rolling cleanup, grow the recordings volume unbounded, and leak the
	// protected map. Segments still referenced by another archive are retained
	// (isSegmentInOtherArchive), and this archive excludes itself. (#76)
	defer am.unprotectSegments(streamKey, from, to, archiveID)

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

	am.markCompleted(archiveID, info.Size(), outFile)

	log.Printf("[archive] Completed: %s (%d bytes, %d segments)", archiveID, info.Size(), len(segments))
}

// isTerminalStatus reports whether a status is a terminal (never-reverting)
// archive state. The two terminal states are `completed` and `failed`; the other
// four (protecting/pending/finalizing/processing) are in-progress
// (archive-download-ux 단위A 출력계약·핵심로직 단조성).
func isTerminalStatus(status string) bool {
	return status == "completed" || status == "failed"
}

// markCompleted atomically moves an archive to `completed`, recording status,
// sizeBytes, filePath AND completedAt (now, UTC RFC3339) under one write lock, so
// a consumer never observes a completed archive missing any of those three
// (단위A 핵심로직 "동시적 불변식", 단언 A3). It is a NO-OP when the archive is already
// terminal (completed/failed), enforcing monotonicity — e.g. a `failed` archive
// must never become `completed` (단언 A7). (#archive-download-ux)
func (am *ArchiveManager) markCompleted(archiveID string, sizeBytes int64, filePath string) {
	am.mu.Lock()
	defer am.mu.Unlock()
	for i, a := range am.archives {
		if a.ID == archiveID {
			if isTerminalStatus(a.Status) {
				return // terminal states are frozen (monotonicity, A7)
			}
			now := time.Now().UTC().Format(time.RFC3339)
			am.archives[i].Status = "completed"
			am.archives[i].SizeBytes = sizeBytes
			am.archives[i].FilePath = filePath
			am.archives[i].CompletedAt = &now
			am.saveMetadata()
			return
		}
	}
}

// updateStatus transitions an archive's status (and, for failures, its lastError
// reason). Terminal archives are frozen: once `completed` or `failed`, an archive
// never moves to a different status (단조성, 단언 A7) — a completed archive must not
// fall back to an in-progress state, and a failed archive stays failed.
func (am *ArchiveManager) updateStatus(archiveID, status, errMsg string) {
	am.mu.Lock()
	defer am.mu.Unlock()
	// Invariant guard: every `failed` transition carries a non-empty lastError
	// (단언 A4 — 모든 failed 종단 전이 → non-empty lastError). The wire field uses
	// `omitempty`, so an empty reason would silently drop the field; substitute a
	// human-readable default rather than let A4's invariant break unobserved.
	if status == "failed" && errMsg == "" {
		errMsg = "archive failed (no reason reported)"
	}
	for i, a := range am.archives {
		if a.ID == archiveID {
			if isTerminalStatus(a.Status) {
				return // terminal states never revert (monotonicity, A7)
			}
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

// downloadGateCode is the pure download-gate decision for
// GET /api/archives/{id}/download: 200 when the archive exists and is `completed`
// (safe to serve完결 media), 409 when it exists but is non-completed (미완료 4종 및
// failed — no partial/0-byte media served), 404 when absent. The HTTP handler
// delegates to this so the gate is unit-judgeable (단언 A5/A6/A8). (#archive-download-ux)
func downloadGateCode(am *ArchiveManager, id string) int {
	for _, a := range am.ListArchives() {
		if a.ID == id {
			if a.Status == "completed" {
				return http.StatusOK
			}
			return http.StatusConflict
		}
	}
	return http.StatusNotFound
}

// errArchiveNotFound is returned by DeleteArchive when no archive with the given
// ID exists. It is a sentinel so callers (e.g. evictIDs) can distinguish a benign
// concurrent-delete race from a genuine deletion failure via errors.Is.
var errArchiveNotFound = errors.New("archive not found")

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
		return fmt.Errorf("%w: %s", errArchiveNotFound, archiveID)
	}

	archive := am.archives[idx]

	// Remove the archive directory
	archiveDir := filepath.Join(am.archivesDir, archiveID)
	os.RemoveAll(archiveDir)

	// Unprotect segments that were in this archive's range (exclude this archive
	// itself — it is still in the slice at this point). (#76)
	if archive.StreamKey != "" {
		fromTime, _ := time.Parse(time.RFC3339, archive.From)
		toTime, _ := time.Parse(time.RFC3339, archive.To)
		if !fromTime.IsZero() && !toTime.IsZero() {
			am.unprotectSegments(archive.StreamKey, fromTime, toTime, archive.ID)
		}
	}

	// Remove from list
	am.archives = append(am.archives[:idx], am.archives[idx+1:]...)
	am.saveMetadata()

	log.Printf("[archive] Deleted: %s", archiveID)
	return nil
}

// parseEvictInt64 parses an ARCHIVE_MAX_BYTES-style env value. It returns the
// effective value (0 = disabled) and whether the raw value is a MISCONFIGURATION
// worth warning about. Empty/unset and an explicit "0" are legitimate disables
// per the spec input table ("0 또는 미설정 → 비활성") and never warn; a non-empty
// value that is neither "0" nor a positive integer (e.g. "100GiB", "abc", "-5")
// is silently ignored today, so it returns warn=true to surface the footgun.
func parseEvictInt64(raw string) (int64, bool) {
	if raw == "" || raw == "0" {
		return 0, false
	}
	if v, err := strconv.ParseInt(raw, 10, 64); err == nil && v > 0 {
		return v, false
	}
	return 0, true
}

// parseEvictInt is the int counterpart for ARCHIVE_RETENTION_DAYS. Same warn
// contract as parseEvictInt64.
func parseEvictInt(raw string) (int, bool) {
	if raw == "" || raw == "0" {
		return 0, false
	}
	if v, err := strconv.Atoi(raw); err == nil && v > 0 {
		return v, false
	}
	return 0, true
}

// evictionPlan is the full, side-effect-free result of a retention pass: the
// victim ids to delete plus the diagnostics EvictArchives needs to log (with
// dedup). It carries no logging itself so the selection core stays pure.
type evictionPlan struct {
	victims []string // deduped, deterministic oldest-first order
	// floorID is the newest capacity-preserved target that alone still exceeds
	// the cap and genuinely SURVIVES this pass ("" if none). It is the subject of
	// the "floor preserved" warning.
	floorID string
	// unparsableIDs are target-shaped archives (completed ∧ IncidentTime!="")
	// whose CreatedAt could not be parsed, so they were excluded from selection
	// (never mis-deleted). Sorted for deterministic diagnostics.
	unparsableIDs []string
}

// planEvictions is the pure, total, side-effect-free core of the archive
// retention policy (archive-retention-policy.md): given the archive list, the
// capacity cap (bytes) and age cap (days), it computes the victim set AND the
// diagnostics, deleting nothing and logging nothing.
//
// Target set = Status=="completed" ∧ IncidentTime!="" ∧ CreatedAt parseable as
// RFC3339. Everything else (manual/non-auto/in-flight/failed/legacy/unparseable)
// is excluded from capacity accounting, age accounting and deletion. maxBytes<=0
// disables the capacity policy; retentionDays<=0 disables the age policy. The
// two eviction sets are unioned (deduped).
func planEvictions(archives []ArchiveMetadata, maxBytes int64, retentionDays int, now time.Time) evictionPlan {
	type target struct {
		id      string
		created time.Time
		size    int64
	}
	var targets []target
	var unparsable []string
	for _, a := range archives {
		if a.Status != "completed" || a.IncidentTime == "" {
			continue
		}
		created, err := time.Parse(time.RFC3339, a.CreatedAt)
		if err != nil {
			// Never mis-delete evidence on an unparseable timestamp — record it for
			// the (deduped) diagnostic emitted by the stateful wrapper.
			unparsable = append(unparsable, a.ID)
			continue
		}
		targets = append(targets, target{id: a.ID, created: created, size: a.SizeBytes})
	}
	sort.Strings(unparsable)

	// Deterministic oldest-first order: (CreatedAt asc, ID asc).
	sort.Slice(targets, func(i, j int) bool {
		if targets[i].created.Equal(targets[j].created) {
			return targets[i].id < targets[j].id
		}
		return targets[i].created.Before(targets[j].created)
	})

	evict := make(map[string]bool)

	// Age policy (secondary): evict every target older than the retention window.
	// Ignores the capacity floor — even a sole/newest target over retention goes.
	if retentionDays > 0 {
		cutoff := now.Add(-time.Duration(retentionDays) * 24 * time.Hour)
		for _, tg := range targets {
			if tg.created.Before(cutoff) {
				evict[tg.id] = true
			}
		}
	}

	// Capacity policy (primary): if the target total exceeds the cap, delete
	// oldest-first until within the cap, but always keep the newest 1 (floor).
	var floorID string
	if maxBytes > 0 {
		var total int64
		for _, tg := range targets {
			total += tg.size
		}
		for i := 0; i < len(targets)-1 && total > maxBytes; i++ {
			evict[targets[i].id] = true
			total -= targets[i].size
		}
		// Single-archive floor: capacity kept the newest target even though it alone
		// exceeds the cap. It is the floor subject ONLY when it actually survives the
		// final eviction decision — if the age policy also evicts it, it is truly
		// deleted and there is no floor to warn about.
		if total > maxBytes && len(targets) > 0 {
			newest := targets[len(targets)-1]
			if !evict[newest.id] {
				floorID = newest.id
			}
		}
	}

	// Victims in deterministic oldest-first order, deduped by the map.
	victims := make([]string, 0, len(evict))
	for _, tg := range targets {
		if evict[tg.id] {
			victims = append(victims, tg.id)
		}
	}
	return evictionPlan{victims: victims, floorID: floorID, unparsableIDs: unparsable}
}

// selectEvictions is the pure victim selector — the assertion-gate entry point.
// It returns the deduped set of archive IDs to evict in deterministic
// oldest-first order and, like planEvictions, has zero side effects (no logging,
// no deletion). Diagnostics live on the stateful EvictArchives wrapper.
func selectEvictions(archives []ArchiveMetadata, maxBytes int64, retentionDays int, now time.Time) []string {
	return planEvictions(archives, maxBytes, retentionDays, now).victims
}

// floorWarnTransition decides whether a "floor preserved" warning should be
// emitted this cycle, given the previously-warned floor id and the current one,
// and returns the next state to store. It fires only on a CHANGE to a non-empty
// floor id, so a persistent floor condition is logged once, not every cycle; a
// cleared ("") or changed floor resets/re-arms the warning. Pure — unit-testable.
func floorWarnTransition(prev, cur string) (warn bool, next string) {
	return cur != "" && cur != prev, cur
}

// newUnparsableWarnings returns the ids to warn about this cycle (present now but
// not previously warned) and the next warned-set state. A persistent set is
// warned once; ids that disappear are dropped so a later reappearance re-warns.
// Pure — unit-testable.
func newUnparsableWarnings(prev map[string]bool, cur []string) (toWarn []string, next map[string]bool) {
	next = make(map[string]bool, len(cur))
	for _, id := range cur {
		next[id] = true
		if !prev[id] {
			toWarn = append(toWarn, id)
		}
	}
	return toWarn, next
}

// evictIDs deletes each id via the shared DeleteArchive path (directory removal
// + segment unprotect + atomic metadata save). Only a not-found
// (errArchiveNotFound — the snapshot's target was concurrently DELETEd before
// its turn) is absorbed as benign and the cycle continues (eviction is
// idempotent). Any OTHER error is surfaced at error level (self-heal retries on
// the next cycle) and does not stop the loop.
func (am *ArchiveManager) evictIDs(ids []string) {
	for _, id := range ids {
		err := am.DeleteArchive(id)
		switch {
		case err == nil:
			log.Printf("[archive] retention: evicted %s", id)
		case errors.Is(err, errArchiveNotFound):
			// Concurrent DELETE won the race — the eviction goal is already met.
			log.Printf("[archive] retention: %s already gone (concurrent delete), continuing", id)
		default:
			// Genuine failure — surface it (do NOT mislabel as "already gone").
			log.Printf("[archive] retention: ERROR evicting %s: %v (will retry next cycle)", id, err)
		}
	}
}

// EvictArchives runs one retention cycle: snapshot the archive list under the
// lock, release it, compute the eviction plan purely, emit deduped diagnostics,
// then delete each victim via DeleteArchive (which takes the lock itself — no
// re-entrancy, no deletion while holding the lock). now is injectable for
// deterministic age/self-heal judging.
func (am *ArchiveManager) EvictArchives(now time.Time) {
	if am.evictMaxBytes <= 0 && am.evictRetentionDays <= 0 {
		return // both policies disabled → no-op (opt-in unbounded retention)
	}
	am.mu.RLock()
	snapshot := make([]ArchiveMetadata, len(am.archives))
	copy(snapshot, am.archives)
	am.mu.RUnlock()

	plan := planEvictions(snapshot, am.evictMaxBytes, am.evictRetentionDays, now)
	am.reportEvictionDiagnostics(plan)
	am.evictIDs(plan.victims)
}

// reportEvictionDiagnostics emits the floor-preserved and unparseable-CreatedAt
// warnings with change-detection so a persistent condition is logged once, not
// every 30s ticker cycle. Called from the single ticker goroutine, so its state
// fields need no extra locking.
func (am *ArchiveManager) reportEvictionDiagnostics(plan evictionPlan) {
	warn, nextFloor := floorWarnTransition(am.lastFloorWarnedID, plan.floorID)
	if warn {
		log.Printf("[archive] retention: capacity still over limit after keeping newest target %s — floor preserved (single archive exceeds ARCHIVE_MAX_BYTES)", plan.floorID)
	}
	am.lastFloorWarnedID = nextFloor

	toWarn, next := newUnparsableWarnings(am.warnedUnparsableIDs, plan.unparsableIDs)
	for _, id := range toWarn {
		log.Printf("[archive] retention: WARNING archive %s has an unparseable CreatedAt — excluded from eviction (not deleted)", id)
	}
	am.warnedUnparsableIDs = next
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

		// Unprotect segments (exclude this archive — still in the slice here). (#76)
		if archive.StreamKey != "" {
			fromTime, _ := time.Parse(time.RFC3339, archive.From)
			toTime, _ := time.Parse(time.RFC3339, archive.To)
			if !fromTime.IsZero() && !toTime.IsZero() {
				am.unprotectSegments(archive.StreamKey, fromTime, toTime, archive.ID)
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
	// Reject traversal-bearing identifiers before they are joined into paths. (#73)
	if !isValidPathComponent(incidentID) {
		log.Printf("[archive] Rejecting invalid incidentId %q", incidentID)
		return created
	}
	for _, streamKey := range streamKeys {
		if !isValidPathComponent(streamKey) {
			log.Printf("[archive] Skipping invalid streamKey %q", streamKey)
			continue
		}
		archiveID := fmt.Sprintf("%s_%s_%s", incidentID, streamKey, protectFrom.UTC().Format("20060102_150405"))

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

		// Duplicate check + append under one write lock: concurrent protect
		// requests for the same incident must not insert the same archiveID
		// twice (which would corrupt later index-based status/delete logic). (#79)
		am.mu.Lock()
		var existing *ArchiveMetadata
		for i := range am.archives {
			if am.archives[i].ID == archiveID {
				dup := am.archives[i]
				existing = &dup
				break
			}
		}
		if existing != nil {
			am.mu.Unlock()
			created = append(created, *existing)
			continue
		}
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

// recoveryTargets returns the archives that need startup recovery: non-terminal
// (status ∈ {pending, processing, finalizing}) and NOT "protecting". Terminal
// states (completed/failed) and "protecting" (handled by RefreshProtection) are
// excluded. See docs/spec/recording.md §핵심 로직 7 + 단언 P/P-2.
func recoveryTargets(archives []ArchiveMetadata) []ArchiveMetadata {
	var out []ArchiveMetadata
	for _, a := range archives {
		switch a.Status {
		case "pending", "processing", "finalizing":
			out = append(out, a)
		}
	}
	return out
}

// RecoverArchives performs startup recovery for in-flight (non-terminal, non-
// protecting) archives after metadata load. It enforces the ordering contract of
// §핵심 로직 7: ① metadata already loaded → ② re-establish deletion protection for
// every recovery target's [from, to) segment range (SYNCHRONOUSLY, before the
// rolling cleanup loop's first run) → then resume each merge. This guarantees the
// originals a resuming merge needs are not deleted by cleanup as a restart side
// effect.
//
// The protection pass logs "Recovery protection re-established"; the cleanup loop
// logs "Rolling cleanup started" on its first run. Because this runs synchronously
// in main() before the cleanup goroutine is started, the former always precedes
// the latter (a simple ordering invariant — no locks/barriers required).
//
// Resume direction: with protection re-established, each merge is re-run. A valid
// range (segments present in [from, to)) converges to completed; a range whose
// required .ts files are all gone (or that errors) is forced to terminal failed
// with a non-empty lastError. No archive stays stuck in a non-terminal state
// across restart.
func (am *ArchiveManager) RecoverArchives() {
	targets := am.reestablishRecoveryProtection()

	// Resume merges. processArchive converges each to a terminal state:
	// completed (segments present + merge ok) or failed (segments gone / error).
	for _, a := range targets {
		fromTime, _ := time.Parse(time.RFC3339, a.From)
		toTime, _ := time.Parse(time.RFC3339, a.To)
		if fromTime.IsZero() || toTime.IsZero() {
			// Unrecoverable metadata: force terminal failed with reason.
			am.updateStatus(a.ID, "failed", "recovery: archive has invalid from/to range")
			log.Printf("[archive] Recovery: %s has invalid range, marked failed", a.ID)
			continue
		}
		log.Printf("[archive] Recovery: resuming merge for %s [%s, %s)", a.ID, a.From, a.To)
		go am.processArchive(a.ID, a.StreamKey, fromTime, toTime)
	}
}

// reestablishRecoveryProtection performs step ② of the startup ordering contract:
// synchronously register the [from, to) segment range of every recovery target
// into the protected set, then log the "Recovery protection re-established" marker.
// It returns the recovery targets (for the caller to resume). This is separated
// from the async merge resume so the ordering/protection invariant is observable
// without launching background work.
func (am *ArchiveManager) reestablishRecoveryProtection() []ArchiveMetadata {
	am.mu.RLock()
	targets := recoveryTargets(am.archives)
	am.mu.RUnlock()

	for _, a := range targets {
		fromTime, _ := time.Parse(time.RFC3339, a.From)
		toTime, _ := time.Parse(time.RFC3339, a.To)
		if fromTime.IsZero() || toTime.IsZero() {
			continue
		}
		am.protectSegmentsInRange(a.StreamKey, fromTime, toTime)
	}
	log.Printf("[archive] Recovery protection re-established for %d recovery-target archive(s)", len(targets))
	return targets
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

// unprotectSegments releases protection on the .ts segments in [from,to] for a
// stream, skipping any still referenced by another (non-excluded) archive. The
// excludeArchiveID lets a caller ignore its own archive entry — otherwise the
// archive being completed/deleted would still be found by isSegmentInOtherArchive
// and its segments would stay protected forever. (#76)
func (am *ArchiveManager) unprotectSegments(streamKey string, from, to time.Time, excludeArchiveID string) {
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
			if !am.isSegmentInOtherArchive(fullPath, excludeArchiveID) {
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
	// Serve directory sizes from a short-TTL cache so a polling UI does not walk
	// the entire recordings/archives tree on every request. (#81)
	var recordingsSize, archivesSize int64
	if am.recSizeCache != nil {
		recordingsSize = am.recSizeCache.get(func() int64 { return dirSize(am.recordingsDir) })
	} else {
		recordingsSize = dirSize(am.recordingsDir)
	}
	if am.arcSizeCache != nil {
		archivesSize = am.arcSizeCache.get(func() int64 { return dirSize(am.archivesDir) })
	} else {
		archivesSize = dirSize(am.archivesDir)
	}

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
	err := filepath.Walk(path, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			// Log rather than silently swallow per-entry walk errors (#81).
			log.Printf("[storage] walk error at %s: %v", p, err)
			return nil
		}
		if info.IsDir() {
			return nil
		}
		size += info.Size()
		return nil
	})
	if err != nil {
		log.Printf("[storage] walk failed for %s: %v", path, err)
	}
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

	// Archive retention thresholds (archive-retention-policy.md). Same env
	// convention as ROLLING_WINDOW_MINUTES (strconv + >0 guard); unset/non-positive
	// disables that policy (opt-in).
	rawMaxBytes := os.Getenv("ARCHIVE_MAX_BYTES")
	archiveMaxBytes, warnMaxBytes := parseEvictInt64(rawMaxBytes)
	if warnMaxBytes {
		// Non-empty, not an explicit "0", and not a positive integer (e.g. a typo
		// like "100GiB"): surface it so the value is not silently treated as
		// disabled, which would quietly regress to unbounded archive growth.
		log.Printf("[archive] WARNING: ARCHIVE_MAX_BYTES=%q is not \"0\" or a positive integer (bytes) — capacity eviction stays DISABLED", rawMaxBytes)
	}
	rawRetentionDays := os.Getenv("ARCHIVE_RETENTION_DAYS")
	archiveRetentionDays, warnRetentionDays := parseEvictInt(rawRetentionDays)
	if warnRetentionDays {
		log.Printf("[archive] WARNING: ARCHIVE_RETENTION_DAYS=%q is not \"0\" or a positive integer (days) — age eviction stays DISABLED", rawRetentionDays)
	}

	manager := NewRecordingManager(rtmpBaseURL, recordingsDir, ffmpegTimeout)
	archiveManager := NewArchiveManager(archivesDir, recordingsDir, manager)
	archiveManager.evictMaxBytes = archiveMaxBytes
	archiveManager.evictRetentionDays = archiveRetentionDays
	log.Printf("Archive retention config (max bytes: %d, retention days: %d; 0 = disabled)", archiveMaxBytes, archiveRetentionDays)

	// Startup recovery for in-flight archives. MUST run before the rolling cleanup
	// loop starts so recovery-target segments are protected before the first
	// cleanup (§핵심 로직 7 ordering contract; logs "Recovery protection
	// re-established" strictly before "Rolling cleanup started"). Non-terminal,
	// non-protecting archives resume merging and converge to completed/failed.
	archiveManager.RecoverArchives()

	// Fetch initial camera list from web-backend (retry on failure)
	reloadClient := &http.Client{Timeout: 10 * time.Second}
	var cameras []CameraInfo
	for attempt := 1; attempt <= 10; attempt++ {
		fetched, ferr := fetchCameras(reloadClient, webBackendURL)
		if ferr != nil {
			log.Printf("Failed to fetch cameras (attempt %d/10): %v", attempt, ferr)
		} else {
			cameras = fetched
		}
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
		first := true
		for range ticker.C {
			if first {
				// Ordering marker (§핵심 로직 7): recovery protection is always
				// re-established before this first cleanup run.
				log.Println("Rolling cleanup started")
				first = false
			}
			manager.CleanupOldSegments(rollingWindow)
		}
	}()

	// Start protection refresh + auto-finalize + archive retention goroutine
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		first := true
		for range ticker.C {
			if first {
				// Ordering marker (sibling of "Rolling cleanup started"): makes the
				// ticker→EvictArchives wiring observable.
				log.Println("Archive retention started")
				first = false
			}
			// Re-protect new segments for active incidents
			archiveManager.RefreshProtection()
			// Auto-finalize incidents protecting for > 2 hours
			archiveManager.AutoFinalizeExpired(2 * time.Hour)
			// Evict archives past the capacity/age caps (self-heals each cycle).
			archiveManager.EvictArchives(time.Now().UTC())
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

		cameras, err := fetchCameras(reloadClient, webBackendURL)
		if err != nil {
			// web-backend unreachable / untrustworthy: preserve the existing
			// recorder set rather than tearing everything down. Only an explicit
			// 200 + array (possibly empty) is allowed to drive a reconcile that
			// stops recorders (⚠️ 8). Aligns with cctv-adapter (502) / youtube-adapter (500).
			log.Printf("[reload] Failed to fetch cameras, preserving existing recorders: %v", err)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadGateway)
			json.NewEncoder(w).Encode(map[string]string{"error": "failed to fetch cameras from web-backend"})
			return
		}
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

		// Validate streamKey (previously only filename was checked — asymmetric). (#73)
		if !isValidPathComponent(streamKey) {
			http.Error(w, "invalid stream key", http.StatusBadRequest)
			return
		}

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

		// Reject traversal-bearing identifiers up front. (#73)
		if req.IncidentID != "" && !isValidPathComponent(req.IncidentID) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]string{"error": "invalid incidentId"})
			return
		}
		for _, sk := range req.StreamKeys {
			if !isValidPathComponent(sk) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusBadRequest)
				json.NewEncoder(w).Encode(map[string]string{"error": "invalid streamKey"})
				return
			}
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

		// Reject traversal-bearing identifiers up front. (#73)
		if !isValidPathComponent(req.IncidentID) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]string{"error": "invalid incidentId"})
			return
		}
		for _, sk := range req.StreamKeys {
			if !isValidPathComponent(sk) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusBadRequest)
				json.NewEncoder(w).Encode(map[string]string{"error": "invalid streamKey"})
				return
			}
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

	// GET /api/archives/{id}/download — serve archive MP4 file.
	// Download gate (단위A 출력계약 + API 계약 델타): completed → 2xx video/mp4;
	// any existing but non-completed archive (미완료 4종 및 failed) → 409; absent → 404.
	// The decision is factored into downloadGateCode so it is unit-judgeable and no
	// partial/0-byte media ever leaks for a non-completed archive (단언 A5/A6/A8).
	mux.HandleFunc("GET /api/archives/{id}/download", func(w http.ResponseWriter, r *http.Request) {
		archiveID := r.PathValue("id")
		switch downloadGateCode(archiveManager, archiveID) {
		case http.StatusOK:
			for _, a := range archiveManager.ListArchives() {
				if a.ID == archiveID {
					w.Header().Set("Content-Type", "video/mp4")
					w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"%s.mp4\"", archiveID))
					http.ServeFile(w, r, a.FilePath)
					return
				}
			}
			// Raced away between the gate decision and the serve: treat as absent.
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusNotFound)
			json.NewEncoder(w).Encode(map[string]string{"error": "archive not found"})
		case http.StatusConflict:
			status := ""
			for _, a := range archiveManager.ListArchives() {
				if a.ID == archiveID {
					status = a.Status
					break
				}
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusConflict)
			json.NewEncoder(w).Encode(map[string]string{"error": "archive not ready", "status": status})
		default:
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusNotFound)
			json.NewEncoder(w).Encode(map[string]string{"error": "archive not found"})
		}
	})

	// GET /api/storage — disk usage stats
	mux.HandleFunc("GET /api/storage", func(w http.ResponseWriter, r *http.Request) {
		stats := archiveManager.GetStorageStats()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(stats)
	})

	srv := newHTTPServer(mux)

	// Graceful shutdown (#40): on SIGTERM/SIGINT terminate the ffmpeg recorder
	// children through manager.Stop() (SIGTERM → grace → SIGKILL) so in-progress
	// segments are flushed rather than truncated/left 0-byte, then drain in-flight
	// HTTP downloads via srv.Shutdown. Without this, docker stop kills the process
	// immediately and orphans partial segments.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		<-sigCh
		log.Println("shutting down: stopping recorders and draining HTTP...")
		manager.Stop()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := srv.Shutdown(ctx); err != nil {
			log.Printf("http shutdown: %v", err)
		}
	}()

	log.Println("recording service listening on :8080")
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		manager.Stop()
		log.Fatal(err)
	}
}

// newHTTPServer builds the service HTTP server with hardened timeouts. Without
// them ReadHeaderTimeout/ReadTimeout/IdleTimeout default to 0 (unlimited) and a
// slow/malicious client can trickle headers or body to hold goroutines/sockets
// open indefinitely (Slowloris). WriteTimeout is deliberately left at 0
// (unlimited): this service streams large archive/segment downloads via
// http.ServeFile and a hard write deadline would truncate legitimate transfers.
func newHTTPServer(handler http.Handler) *http.Server {
	return &http.Server{
		Addr:              ":8080",
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
}

// fetchCameras retrieves the camera list from web-backend.
//
// A trustworthy result (non-nil error == nil) is returned ONLY on an explicit
// HTTP 200 with a decodable array body. Connection errors, non-200 status codes,
// and body-decode failures all return an error so callers can distinguish an
// "unreachable / untrustworthy" web-backend from a genuine "zero cameras" (which
// is an explicit 200 + empty array). This prevents a transient web-backend outage
// from being misread as "no cameras" and tearing down every recorder (⚠️ 8).
func fetchCameras(client *http.Client, webBackendURL string) ([]CameraInfo, error) {
	resp, err := client.Get(webBackendURL + "/internal/cameras")
	if err != nil {
		return nil, fmt.Errorf("fetch cameras: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("fetch cameras: status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var raw []struct {
		Name       string `json:"name"`
		StreamKey  string `json:"streamKey"`
		SourceType string `json:"sourceType"`
		SourceURL  string `json:"sourceUrl"`
		Enabled    bool   `json:"enabled"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, fmt.Errorf("decode cameras: %w", err)
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
	return cameras, nil
}
