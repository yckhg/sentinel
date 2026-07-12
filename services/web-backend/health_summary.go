package main

import (
	"database/sql"
	"log"
	"net/http"
	"sort"
	"time"
)

// -----------------------------------------------------------------------------
// GET /api/health/summary — 현재-상태 요약 창의 집계 응답 (spec system-status-aggregate).
//
// Shape (spec 출력(계약)):
//
//	{
//	  "summary":  {"healthy": N, "abnormal": M, "offline": K},
//	  "services": [ {"id": "<name>", "status": "healthy"|"unhealthy"}, ... ], // 고정 전체
//	  "exceptions": [ {"id","displayName","category","ageSec","reason"}, ... ], // cap 50
//	  "exceptionsOverflow": <남은 예외 수, 상한 이하면 0>
//	}
//
// Boundary invariant (A/I/J): 세 카운트의 합 = 미삭제 장비 총수. 개별 항목 수는
// min(abnormal+offline, cap)로 캡되고 정상 수에 불변이다. 카운트는 SQL
// COUNT/GROUP BY로 산출되어 정상 장비를 개별 실체화하지 않는다. 카운트와 예외
// 목록은 단일 읽기 트랜잭션(단일 일관 스냅샷)에서 산출된다.
// -----------------------------------------------------------------------------

// summaryExceptionsCap is the spec default exceptions cap (기본 50건).
const summaryExceptionsCap = 50

type healthSummaryCounts struct {
	Healthy  int `json:"healthy"`
	Abnormal int `json:"abnormal"`
	Offline  int `json:"offline"`
}

type healthSummaryService struct {
	ID     string `json:"id"`
	Status string `json:"status"`
}

type healthSummaryException struct {
	ID          string `json:"id"`
	DisplayName string `json:"displayName"`
	Category    string `json:"category"`
	AgeSec      int64  `json:"ageSec"`
	Reason      string `json:"reason"`
}

type healthSummaryResponse struct {
	Summary            healthSummaryCounts      `json:"summary"`
	Services           []healthSummaryService   `json:"services"`
	Exceptions         []healthSummaryException `json:"exceptions"`
	ExceptionsOverflow int                      `json:"exceptionsOverflow"`
}

// serviceSnapshot returns the full fixed service set (계약 12 어휘 healthy|unhealthy),
// read from the monitor's in-memory entries. The set is always complete regardless
// of device count (assertion F). Sorted by id for a deterministic response.
func (m *HealthMonitor) serviceSnapshot() []healthSummaryService {
	m.mu.RLock()
	out := make([]healthSummaryService, 0, len(m.entries))
	for _, e := range m.entries {
		if e.Kind != KindService {
			continue
		}
		out = append(out, healthSummaryService{ID: e.ID, Status: e.Status})
	}
	m.mu.RUnlock()
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// handleGetHealthSummary handles GET /api/health/summary. It derives device
// categories directly from the devices table (not from monitor-populated sensor
// entries) so a stopped device-status source is absorbed as offline aggregation,
// not a 5xx (assertion K). Categories:
//
//	offline  : now - last_seen > sensor_alive_threshold_sec (미생존)
//	abnormal : 생존(<=threshold) AND alert_state == "active"
//	healthy  : 생존 AND alert_state != "active"
func handleGetHealthSummary(mon *HealthMonitor) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Runtime threshold (계약 11) — re-read each request so PUT /api/settings
		// changes take effect without restart (assertion H).
		threshold := mon.readIntSetting("health.sensor_alive_threshold_sec", 60)

		// Freeze a single reference `now` (unix seconds) in Go and BIND it to BOTH
		// queries. SQLite only freezes `'now'` within one statement, not across
		// statements in a transaction — so inline strftime('%s','now') in each query
		// could evaluate at two different instants and let a device cross the offline
		// threshold between the counts read and the exceptions read (mis-derived
		// exceptionsOverflow, or an exceptions row absent from the offline count). A
		// bound `now` gives counts and exceptions ONE consistent instant (assertion
		// A/I: "카운트와 예외 목록이 서로 다른 시점을 보지 않는다").
		now := time.Now().Unix()

		ctx, cancel := dbCtx(r.Context())
		defer cancel()

		// Single consistent snapshot: counts and exceptions in one read tx, both
		// keyed off the same bound `now`, so they never see different points in time
		// (assertion A/I note).
		tx, err := mon.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
		if err != nil {
			log.Printf("health summary begin tx error: %v", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
			return
		}
		defer tx.Rollback()

		// Counts via set aggregate (SQL SUM/CASE) — healthy devices are never
		// individually materialized. age = (bound now) - last_seen, in whole seconds.
		var counts healthSummaryCounts
		err = tx.QueryRowContext(ctx, `
			SELECT
			  COALESCE(SUM(CASE WHEN age <= ? AND alert_state != 'active' THEN 1 ELSE 0 END), 0),
			  COALESCE(SUM(CASE WHEN age <= ? AND alert_state =  'active' THEN 1 ELSE 0 END), 0),
			  COALESCE(SUM(CASE WHEN age >  ? THEN 1 ELSE 0 END), 0)
			FROM (
			  SELECT alert_state,
			         (? - strftime('%s', last_seen)) AS age
			  FROM devices WHERE deleted_at IS NULL
			)
		`, threshold, threshold, threshold, now).Scan(&counts.Healthy, &counts.Abnormal, &counts.Offline)
		if err != nil {
			log.Printf("health summary counts error: %v", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
			return
		}

		// Exceptions: abnormal or offline only, capped, keyed off the SAME bound
		// `now`. Category-aware ordering so the cap never hides the higher-urgency
		// category: abnormal (alive + active alarm, small age) sorts BEFORE stale
		// offline devices. Within each category, most-stale first. exception
		// condition = age>threshold (offline) OR alert_state active (abnormal-while-
		// alive). rank: offline → 1, abnormal → 0.
		exceptions := []healthSummaryException{}
		rows, err := tx.QueryContext(ctx, `
			SELECT site_id, device_id, alias, alert_state, age FROM (
			  SELECT site_id, device_id, alias, alert_state,
			         (? - strftime('%s', last_seen)) AS age
			  FROM devices WHERE deleted_at IS NULL
			)
			WHERE age > ? OR alert_state = 'active'
			ORDER BY (CASE WHEN age > ? THEN 1 ELSE 0 END) ASC, age DESC, site_id ASC, device_id ASC
			LIMIT ?
		`, now, threshold, threshold, summaryExceptionsCap)
		if err != nil {
			log.Printf("health summary exceptions error: %v", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
			return
		}
		for rows.Next() {
			var siteID, deviceID, alias, alertState string
			var age int64
			if err := rows.Scan(&siteID, &deviceID, &alias, &alertState, &age); err != nil {
				log.Printf("health summary scan exception error: %v", err)
				continue
			}
			displayName := alias
			if displayName == "" {
				displayName = deviceID
			}
			category := "abnormal"
			reason := "alert active"
			if age > int64(threshold) {
				category = "offline"
				reason = "no heartbeat"
			}
			exceptions = append(exceptions, healthSummaryException{
				ID:          siteID + ":" + deviceID,
				DisplayName: displayName,
				Category:    category,
				AgeSec:      age,
				Reason:      reason,
			})
		}
		rows.Close()
		if err := tx.Commit(); err != nil {
			log.Printf("health summary commit error: %v", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
			return
		}

		// Overflow = total exceptions (== abnormal + offline) beyond the cap. Derived
		// from the exact counts so it stays consistent with the capped list.
		overflow := counts.Abnormal + counts.Offline - len(exceptions)
		if overflow < 0 {
			overflow = 0
		}

		writeJSON(w, http.StatusOK, healthSummaryResponse{
			Summary:            counts,
			Services:           mon.serviceSnapshot(),
			Exceptions:         exceptions,
			ExceptionsOverflow: overflow,
		})
	}
}
