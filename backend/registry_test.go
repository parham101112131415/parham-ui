package main

import (
	"encoding/json"
	"testing"
)

func TestRegistryOnlineLogic(t *testing.T) {
	const id = "zz-test-uuid"
	// starts offline
	if userOnline(id) {
		t.Fatal("should start offline")
	}
	if got := activeConnectionTotal(); got != 0 {
		t.Fatalf("total should be 0, got %d", got)
	}
	// activity alone -> online, counted once in total
	noteUserActivity(id)
	if !userOnline(id) {
		t.Fatal("activity should mark online")
	}
	if got := activeConnectionTotal(); got != 1 {
		t.Fatalf("total should be 1 (activity user), got %d", got)
	}
	// live connections take over the count
	connOpened(id)
	connOpened(id)
	if got := userLiveConns(id); got != 2 {
		t.Fatalf("live conns = %d, want 2", got)
	}
	if got := activeConnectionTotal(); got != 2 {
		t.Fatalf("total should be 2 live conns (not 2+activity), got %d", got)
	}
	connClosed(id)
	if got := userLiveConns(id); got != 1 {
		t.Fatalf("live conns = %d, want 1", got)
	}
	connClosed(id)
	connClosed(id) // extra close must not go negative
	if got := userLiveConns(id); got != 0 {
		t.Fatalf("live conns = %d, want 0", got)
	}
	if !userOnline(id) {
		t.Fatal("still online via recent activity")
	}
	if ls := userLastSeen(id); ls == nil {
		t.Fatal("last seen should be set")
	}
	// cleanup so other tests see a clean registry
	regMu.Lock()
	delete(userLastActivity, id)
	regMu.Unlock()
	if userOnline(id) {
		t.Fatal("cleanup failed")
	}
}

func TestOnlineEventJSONShape(t *testing.T) {
	const id = "zz-test-uuid-2"
	b := onlineEventJSON(id)
	if len(b) == 0 {
		t.Fatal("empty event")
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("event is not JSON: %v", err)
	}
	if m["config_uuid"] != id {
		t.Fatalf("config_uuid = %v, want %v", m["config_uuid"], id)
	}
	if _, ok := m["online"]; !ok {
		t.Fatal("missing online field")
	}
	snap := onlineSnapshotJSON()
	if len(snap) == 0 {
		t.Fatal("empty snapshot")
	}
}
