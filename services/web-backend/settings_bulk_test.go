package main

import "testing"

// TestValidateBulkSettings exercises the contract-11 known-key table: unknown
// keys, per-key type/range, and the down>=interval cross-constraint evaluated on
// the resulting final state (assertion V / issue #95).
func TestValidateBulkSettings(t *testing.T) {
	// current DB state used for keys the request does not touch.
	const curInterval, curDown = 30, 90

	cases := []struct {
		name    string
		updates map[string]string
		wantErr bool
	}{
		{
			name:    "all valid health keys",
			updates: map[string]string{"health.service_check_interval_sec": "10", "health.service_down_threshold_sec": "120", "health.sensor_alive_threshold_sec": "45"},
			wantErr: false,
		},
		{
			name:    "valid site_url",
			updates: map[string]string{"site_url": "https://x.example"},
			wantErr: false,
		},
		{
			name:    "unknown key rejected",
			updates: map[string]string{"health.service_check_interval_sec": "10", "totally_unknown": "1"},
			wantErr: true,
		},
		{
			name:    "non-integer value rejected",
			updates: map[string]string{"health.service_check_interval_sec": "abc"},
			wantErr: true,
		},
		{
			name:    "below lower bound rejected",
			updates: map[string]string{"health.service_check_interval_sec": "2"},
			wantErr: true,
		},
		{
			name:    "above upper bound rejected",
			updates: map[string]string{"health.service_check_interval_sec": "5000"},
			wantErr: true,
		},
		{
			name:    "cross-constraint violation (down<interval, both in request)",
			updates: map[string]string{"health.service_check_interval_sec": "100", "health.service_down_threshold_sec": "50"},
			wantErr: true,
		},
		{
			name:    "cross-constraint violation vs current interval (only down in request)",
			updates: map[string]string{"health.service_down_threshold_sec": "10"}, // 10 < current interval 30
			wantErr: true,
		},
		{
			name:    "cross-constraint ok using current down (only interval in request)",
			updates: map[string]string{"health.service_check_interval_sec": "20"}, // 20 <= current down 90
			wantErr: false,
		},
		{
			name:    "site_url non-http scheme rejected",
			updates: map[string]string{"site_url": "ftp://x.example"},
			wantErr: true,
		},
		{
			name:    "site_url empty rejected",
			updates: map[string]string{"site_url": ""},
			wantErr: true,
		},
		{
			name:    "empty batch rejected",
			updates: map[string]string{},
			wantErr: true,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := validateBulkSettings(c.updates, curInterval, curDown)
			if (err != nil) != c.wantErr {
				t.Fatalf("validateBulkSettings(%v) err=%v, wantErr=%v", c.updates, err, c.wantErr)
			}
		})
	}
}

// TestDecodeBulkSettings verifies both accepted body shapes (object and array).
func TestDecodeBulkSettings(t *testing.T) {
	obj, err := decodeBulkSettings([]byte(`{"site_url":"https://x.example","health.service_check_interval_sec":"10"}`))
	if err != nil || obj["site_url"] != "https://x.example" || obj["health.service_check_interval_sec"] != "10" {
		t.Fatalf("object decode failed: %v %v", obj, err)
	}
	arr, err := decodeBulkSettings([]byte(`[{"key":"site_url","value":"https://y.example"}]`))
	if err != nil || arr["site_url"] != "https://y.example" {
		t.Fatalf("array decode failed: %v %v", arr, err)
	}
	if _, err := decodeBulkSettings([]byte(`not json`)); err == nil {
		t.Fatal("expected error for malformed body")
	}
}
