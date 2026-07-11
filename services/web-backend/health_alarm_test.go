package main

import "testing"

// TestHealthAlarmPayload checks the fixed health-sourced system_alarm sub-schema
// (contract 14): envelope {type,message,details} with details
// {entityKind,entityId,status} (assertions O2/O3).
func TestHealthAlarmPayload(t *testing.T) {
	p := healthAlarmPayload(KindSensor, "site1:VOICE-01", StatusUnhealthy)
	if p["type"] != "system_alarm" {
		t.Fatalf("type=%v", p["type"])
	}
	details, ok := p["details"].(map[string]any)
	if !ok {
		t.Fatalf("details wrong type: %T", p["details"])
	}
	if details["entityKind"] != "sensor" || details["entityId"] != "site1:VOICE-01" || details["status"] != "unhealthy" {
		t.Fatalf("details mismatch: %v", details)
	}
}

// TestBroadcastSystemAlarmAdminOnly verifies system_alarm reaches admin WS
// clients only — user/temp receive nothing (assertions O2/O3, contract 14).
func TestBroadcastSystemAlarmAdminOnly(t *testing.T) {
	admin := &wsClient{role: "admin", send: make(chan []byte, 4)}
	user := &wsClient{role: "user", send: make(chan []byte, 4)}
	temp := &wsClient{role: "temp", send: make(chan []byte, 4)}
	for _, c := range []*wsClient{admin, user, temp} {
		hub.register(c)
		defer hub.unregister(c)
	}

	BroadcastSystemAlarm(healthAlarmPayload(KindService, "hw-gateway", StatusUnhealthy))

	if len(admin.send) != 1 {
		t.Fatalf("admin should receive exactly 1 system_alarm, got %d", len(admin.send))
	}
	if len(user.send) != 0 {
		t.Fatalf("user should receive 0 system_alarm, got %d", len(user.send))
	}
	if len(temp.send) != 0 {
		t.Fatalf("temp should receive 0 system_alarm, got %d", len(temp.send))
	}
}
