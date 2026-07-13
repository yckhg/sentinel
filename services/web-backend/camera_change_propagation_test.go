package main

import (
	"database/sql"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

// TDD gates for docs/spec/camera-change-propagation.md.
//
// Coverage map:
//   A   → TestCameraFanout_ThreeConsumers (+ subtests: create/update/delete,
//         failure-tolerance, db-failure-no-dispatch) — always-on in-process
//         httptest gate observing the web-backend fan-out behavior.
//   B1s → TestIncidentsSchema_NoCameraRef_Static /
//         TestDeleteCamera_NoEvidenceCascade_Static — cheap static scans.
//   B2  → TestDeleteCamera_ArchiveEvidencePreserved_B2 — load-bearing SKIP
//         (needs an isolated recording stack + valid media fixture).
//
// Tests run sequentially (no t.Parallel): assertion A mutates the package-global
// consumer URLs (cctvAdapterURL / youtubeAdapterURL / recordingURL) and restores
// them via t.Cleanup, matching the existing notifierURL pattern.

// ---------------------------------------------------------------------------
// Assertion A — 3-consumer success-then-fan-out (in-process httptest gate).
// ---------------------------------------------------------------------------

// reloadInstrument is an httptest.Server that counts POST /api/cameras/reload
// hits, standing in for one of the three reload consumers.
type reloadInstrument struct {
	srv   *httptest.Server
	count int64
}

func newReloadInstrument(t *testing.T) *reloadInstrument {
	t.Helper()
	ri := &reloadInstrument{}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/cameras/reload", func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&ri.count, 1)
		w.WriteHeader(http.StatusOK)
	})
	ri.srv = httptest.NewServer(mux)
	t.Cleanup(ri.srv.Close)
	return ri
}

func (ri *reloadInstrument) hits() int64 { return atomic.LoadInt64(&ri.count) }

// closedInstrumentURL returns the URL of an httptest.Server that has already been
// Closed, so a POST to it is refused (fault-injection: unreachable consumer).
func closedInstrumentURL(t *testing.T) string {
	t.Helper()
	s := httptest.NewServer(http.NewServeMux())
	u := s.URL
	s.Close()
	return u
}

// withConsumerURLs points the three package-global consumer URLs at the given
// values for the duration of the test and restores them afterward.
func withConsumerURLs(t *testing.T, cctv, youtube, recording string) {
	t.Helper()
	prevC, prevY, prevR := cctvAdapterURL, youtubeAdapterURL, recordingURL
	cctvAdapterURL, youtubeAdapterURL, recordingURL = cctv, youtube, recording
	t.Cleanup(func() {
		cctvAdapterURL, youtubeAdapterURL, recordingURL = prevC, prevY, prevR
	})
}

// waitHits polls get() until it reaches >= min or the timeout elapses.
func waitHits(t *testing.T, name string, get func() int64, min int64, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if get() >= min {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("%s: expected >= %d reload hits within %v, got %d", name, min, timeout, get())
}

// seedCamera inserts a camera row with a fixed stream_key and returns its id.
func seedCamera(t *testing.T, db *sql.DB, name, streamKey string) int64 {
	t.Helper()
	res, err := db.Exec(
		"INSERT INTO cameras (name, location, zone, stream_key, source_type, source_url, enabled) VALUES (?, '', '', ?, 'rtsp', '', 1)",
		name, streamKey)
	if err != nil {
		t.Fatalf("seed camera: %v", err)
	}
	id, _ := res.LastInsertId()
	return id
}

const fanoutPollTimeout = 5 * time.Second

func TestCameraFanout_ThreeConsumers(t *testing.T) {
	db := newTestDB(t)

	// create → all three consumers receive a reload.
	t.Run("create", func(t *testing.T) {
		cctv := newReloadInstrument(t)
		yt := newReloadInstrument(t)
		rec := newReloadInstrument(t)
		withConsumerURLs(t, cctv.srv.URL, yt.srv.URL, rec.srv.URL)

		r := adminReq(t, http.MethodPost, "/api/cameras", "", `{"name":"cam-create","sourceType":"rtsp"}`)
		w := httptest.NewRecorder()
		handleCreateCamera(db)(w, r)
		if w.Code != http.StatusCreated {
			t.Fatalf("create: want 201, got %d (%s)", w.Code, w.Body.String())
		}
		waitHits(t, "cctv", cctv.hits, 1, fanoutPollTimeout)
		waitHits(t, "youtube", yt.hits, 1, fanoutPollTimeout)
		waitHits(t, "recording", rec.hits, 1, fanoutPollTimeout)
	})

	// update → all three consumers receive a reload.
	t.Run("update", func(t *testing.T) {
		id := seedCamera(t, db, "cam-upd", "cam-upd-key")
		cctv := newReloadInstrument(t)
		yt := newReloadInstrument(t)
		rec := newReloadInstrument(t)
		withConsumerURLs(t, cctv.srv.URL, yt.srv.URL, rec.srv.URL)

		r := adminReq(t, http.MethodPut, "/api/cameras/"+strconv.FormatInt(id, 10),
			strconv.FormatInt(id, 10), `{"name":"cam-upd-renamed"}`)
		w := httptest.NewRecorder()
		handleUpdateCamera(db)(w, r)
		if w.Code != http.StatusOK {
			t.Fatalf("update: want 200, got %d (%s)", w.Code, w.Body.String())
		}
		waitHits(t, "cctv", cctv.hits, 1, fanoutPollTimeout)
		waitHits(t, "youtube", yt.hits, 1, fanoutPollTimeout)
		waitHits(t, "recording", rec.hits, 1, fanoutPollTimeout)
	})

	// delete → all three consumers receive a reload.
	t.Run("delete", func(t *testing.T) {
		id := seedCamera(t, db, "cam-del", "cam-del-key")
		cctv := newReloadInstrument(t)
		yt := newReloadInstrument(t)
		rec := newReloadInstrument(t)
		withConsumerURLs(t, cctv.srv.URL, yt.srv.URL, rec.srv.URL)

		r := adminReq(t, http.MethodDelete, "/api/cameras/"+strconv.FormatInt(id, 10),
			strconv.FormatInt(id, 10), "__ABSENT__")
		w := httptest.NewRecorder()
		handleDeleteCamera(db)(w, r)
		if w.Code != http.StatusNoContent {
			t.Fatalf("delete: want 204, got %d (%s)", w.Code, w.Body.String())
		}
		waitHits(t, "cctv", cctv.hits, 1, fanoutPollTimeout)
		waitHits(t, "youtube", yt.hits, 1, fanoutPollTimeout)
		waitHits(t, "recording", rec.hits, 1, fanoutPollTimeout)
	})

	// failure tolerance: one consumer (recording) unreachable (closed server) →
	// the other two still receive + CRUD returns 2xx. Exercised across all three
	// verbs (create/update/delete) so the fault-tolerance holds for every handler.
	failureCases := []struct {
		verb     string
		wantCode int
		invoke   func(t *testing.T, db *sql.DB) (int, string) // returns status, body
	}{
		{
			verb:     "create",
			wantCode: http.StatusCreated,
			invoke: func(t *testing.T, db *sql.DB) (int, string) {
				r := adminReq(t, http.MethodPost, "/api/cameras", "", `{"name":"cam-fault-create","sourceType":"rtsp"}`)
				w := httptest.NewRecorder()
				handleCreateCamera(db)(w, r)
				return w.Code, w.Body.String()
			},
		},
		{
			verb:     "update",
			wantCode: http.StatusOK,
			invoke: func(t *testing.T, db *sql.DB) (int, string) {
				id := seedCamera(t, db, "cam-fault-upd", "cam-fault-upd-key")
				r := adminReq(t, http.MethodPut, "/api/cameras/"+strconv.FormatInt(id, 10),
					strconv.FormatInt(id, 10), `{"name":"cam-fault-upd-renamed"}`)
				w := httptest.NewRecorder()
				handleUpdateCamera(db)(w, r)
				return w.Code, w.Body.String()
			},
		},
		{
			verb:     "delete",
			wantCode: http.StatusNoContent,
			invoke: func(t *testing.T, db *sql.DB) (int, string) {
				id := seedCamera(t, db, "cam-fault-del", "cam-fault-del-key")
				r := adminReq(t, http.MethodDelete, "/api/cameras/"+strconv.FormatInt(id, 10),
					strconv.FormatInt(id, 10), "__ABSENT__")
				w := httptest.NewRecorder()
				handleDeleteCamera(db)(w, r)
				return w.Code, w.Body.String()
			},
		},
	}
	for _, fc := range failureCases {
		t.Run("failure_tolerance_"+fc.verb, func(t *testing.T) {
			cctv := newReloadInstrument(t)
			yt := newReloadInstrument(t)
			closed := closedInstrumentURL(t)
			withConsumerURLs(t, cctv.srv.URL, yt.srv.URL, closed)

			code, body := fc.invoke(t, db)
			if code != fc.wantCode {
				t.Fatalf("failure_tolerance %s: want %d despite unreachable recording consumer, got %d (%s)",
					fc.verb, fc.wantCode, code, body)
			}
			waitHits(t, "cctv", cctv.hits, 1, fanoutPollTimeout)
			waitHits(t, "youtube", yt.hits, 1, fanoutPollTimeout)
		})
	}

	// DB write failure (DELETE of a non-existent id → 404) → no consumer receives.
	t.Run("db_failure_no_dispatch", func(t *testing.T) {
		cctv := newReloadInstrument(t)
		yt := newReloadInstrument(t)
		rec := newReloadInstrument(t)
		withConsumerURLs(t, cctv.srv.URL, yt.srv.URL, rec.srv.URL)

		r := adminReq(t, http.MethodDelete, "/api/cameras/999999", "999999", "__ABSENT__")
		w := httptest.NewRecorder()
		handleDeleteCamera(db)(w, r)
		if w.Code != http.StatusNotFound {
			t.Fatalf("db_failure: want 404 for missing id, got %d (%s)", w.Code, w.Body.String())
		}

		// Structural barrier instead of a fixed sleep: issue a *successful* create
		// against fresh sentinel instruments and wait for its fan-out to land. The
		// sentinel dispatch goroutines are spawned strictly after the 404 path has
		// returned, so once a sentinel hit is observed, any (buggy) dispatch from
		// the 404 path — spawned earlier and targeting the still-open cctv/yt/rec
		// servers captured before the re-point — has had at least as long to land.
		scctv := newReloadInstrument(t)
		syt := newReloadInstrument(t)
		srec := newReloadInstrument(t)
		withConsumerURLs(t, scctv.srv.URL, syt.srv.URL, srec.srv.URL)
		r2 := adminReq(t, http.MethodPost, "/api/cameras", "", `{"name":"cam-sentinel","sourceType":"rtsp"}`)
		w2 := httptest.NewRecorder()
		handleCreateCamera(db)(w2, r2)
		if w2.Code != http.StatusCreated {
			t.Fatalf("db_failure sentinel create: want 201, got %d (%s)", w2.Code, w2.Body.String())
		}
		waitHits(t, "sentinel-cctv", scctv.hits, 1, fanoutPollTimeout)
		waitHits(t, "sentinel-youtube", syt.hits, 1, fanoutPollTimeout)
		waitHits(t, "sentinel-recording", srec.hits, 1, fanoutPollTimeout)

		if n := cctv.hits(); n != 0 {
			t.Errorf("db_failure: cctv must not be dispatched on 404, got %d", n)
		}
		if n := yt.hits(); n != 0 {
			t.Errorf("db_failure: youtube must not be dispatched on 404, got %d", n)
		}
		if n := rec.hits(); n != 0 {
			t.Errorf("db_failure: recording must not be dispatched on 404, got %d", n)
		}
	})
}

// ---------------------------------------------------------------------------
// Assertion B1s — incidents are camera-non-referencing, and the delete handler
// touches no evidence store. Static source scans (cheap always-on gate).
// ---------------------------------------------------------------------------

var incidentsCreateRe = regexp.MustCompile(`(?is)CREATE TABLE IF NOT EXISTS incidents\s*\((.*?)\)\s*;`)

func TestIncidentsSchema_NoCameraRef_Static(t *testing.T) {
	// NOTE (scan limit): migration DDL for this service lives entirely in
	// migrations.go (the `migrations` var). If migration definitions are ever
	// split across additional source files, this single-file scan could go
	// false-negative — widen the read set (or move to a runtime schema
	// introspection gate) at that point.
	src, err := os.ReadFile("migrations.go")
	if err != nil {
		t.Fatalf("read migrations.go: %v", err)
	}
	s := string(src)

	// incidents must not carry a camera_id column anywhere (CREATE or ALTER).
	if regexp.MustCompile(`(?i)ALTER\s+TABLE\s+incidents\s+ADD\s+COLUMN\s+camera_id`).MatchString(s) {
		t.Errorf("incidents must not gain a camera_id column")
	}
	if regexp.MustCompile(`(?i)ALTER\s+TABLE\s+incidents\s+ADD\s+COLUMN\s+stream_key`).MatchString(s) {
		t.Errorf("incidents must not gain a stream_key column")
	}

	// Camera-scoped assertions are confined to the incidents CREATE TABLE body:
	// FK/CASCADE/camera_id/stream_key must be absent *from incidents*. This does
	// not forbid legitimate cascades on unrelated (non-camera) tables.
	m := incidentsCreateRe.FindStringSubmatch(s)
	if m == nil {
		t.Fatalf("could not locate CREATE TABLE incidents in migrations.go")
	}
	body := m[1]
	if regexp.MustCompile(`(?i)camera_id`).MatchString(body) {
		t.Errorf("incidents CREATE TABLE must not contain a camera_id column, body:\n%s", body)
	}
	if regexp.MustCompile(`(?i)stream_key`).MatchString(body) {
		t.Errorf("incidents CREATE TABLE must not contain a stream_key column, body:\n%s", body)
	}
	if regexp.MustCompile(`(?i)REFERENCES\s+cameras`).MatchString(body) {
		t.Errorf("incidents CREATE TABLE must not reference cameras, body:\n%s", body)
	}
	// No delete-cascade hanging off a camera reference within incidents.
	if regexp.MustCompile(`(?i)ON\s+DELETE\s+CASCADE`).MatchString(body) {
		t.Errorf("incidents CREATE TABLE must not declare ON DELETE CASCADE, body:\n%s", body)
	}
}

// funcBody extracts the source of the named top-level func from src (from its
// declaration up to the next top-level "func " or EOF).
func funcBody(t *testing.T, src, name string) string {
	t.Helper()
	start := regexp.MustCompile(`(?m)^func ` + regexp.QuoteMeta(name) + `\b`).FindStringIndex(src)
	if start == nil {
		t.Fatalf("could not locate func %s", name)
	}
	rest := src[start[1]:]
	next := regexp.MustCompile(`(?m)^func `).FindStringIndex(rest)
	if next == nil {
		return src[start[0]:]
	}
	return src[start[0] : start[1]+next[0]]
}

func TestDeleteCamera_NoEvidenceCascade_Static(t *testing.T) {
	src, err := os.ReadFile("cameras.go")
	if err != nil {
		t.Fatalf("read cameras.go: %v", err)
	}
	body := funcBody(t, string(src), "handleDeleteCamera")

	// The only DELETE the handler may issue is against cameras.
	for _, m := range regexp.MustCompile(`(?i)DELETE\s+FROM\s+(\w+)`).FindAllStringSubmatch(body, -1) {
		if tbl := m[1]; !regexp.MustCompile(`(?i)^cameras$`).MatchString(tbl) {
			t.Errorf("handleDeleteCamera must only DELETE FROM cameras, found DELETE FROM %s", tbl)
		}
	}
	if regexp.MustCompile(`(?i)\bincidents\b`).MatchString(body) {
		t.Errorf("handleDeleteCamera must not touch incidents")
	}
	if regexp.MustCompile(`(?i)archive`).MatchString(body) {
		t.Errorf("handleDeleteCamera must not touch archive evidence")
	}
}

// ---------------------------------------------------------------------------
// Assertion B2 — delete preserves protected/finalized archive evidence.
// Load-bearing SKIP: requires an isolated recording stack with a real media
// volume and a valid MPEG-TS fixture (protect → finalize → completed). Kept as a
// skeleton, never a green-washed always-on gate.
// ---------------------------------------------------------------------------

func TestDeleteCamera_ArchiveEvidencePreserved_B2(t *testing.T) {
	t.Skip("B2 (load-bearing): requires an isolated recording stack + valid media fixture; " +
		"not runnable in-process. See docs/spec/camera-change-propagation.md assertion B2.")

	// Fixture / observation protocol (SSOT: recording.md), to be driven against a
	// live recording service in an isolated stack:
	//
	//   1. Seed a valid MPEG-TS segment (real RTMP capture or pre-encoded),
	//      filename YYYYMMDD_HHMMSS.ts, timestamp within
	//      [incidentTime-1h, resolvedAt+30min].
	//   2. POST /api/archives/protect (incidentTime).
	//   3. POST /api/archives/finalize (resolvedAt).
	//   4. Poll GET /api/archives until Status=="completed" && sizeBytes>0 for the
	//      camera's stream_key (segments outside the window go "failed").
	//   5. Snapshot GET /api/incidents (result set).
	//   6. DELETE /api/cameras/{id}; if the recording fan-out is unwired in the
	//      test env, POST /api/cameras/reload directly to recording.
	//   7. Poll GET /api/status (recording) until the deleted stream_key's recorder
	//      is gone (recorder may not exist for a non-live fixture — the invariant is
	//      independent of recorder presence).
	//   8. Assert: (a) an archive entry with streamKey == deleted key still lists in
	//      GET /api/archives; (b) its metadata.json entry and the merged MP4 at
	//      FilePath ({ARCHIVES_DIR}/{archiveId}/{streamKey}.mp4) still exist on disk;
	//      (c) GET /api/incidents result set is unchanged.
}
