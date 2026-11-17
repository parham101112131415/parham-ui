package main

import (
	"strings"
	"testing"
	"time"
)

func TestUsageHistorySamplingAndSeries(t *testing.T) {
	State.mu.Lock()
	oldUsers, oldSettings := State.Users, State.Settings
	State.Users = map[string]*User{}
	u := &User{UserID: "hj1", Username: "parham", ConfigUUID: "hj-uuid", Status: "active", TrafficUsedBytes: 5000}
	State.Users[u.UserID] = u
	State.mu.Unlock()
	defer func() {
		State.mu.Lock()
		State.Users, State.Settings = oldUsers, oldSettings
		State.mu.Unlock()
	}()

	// First sample: baseline only, no fake lifetime bar.
	sampleUsageHistory()
	if len(u.UsageHistory) != 0 {
		t.Fatalf("first sample must not backfill history, got %v", u.UsageHistory)
	}
	// Second sample after +3000 bytes: today's bucket gets exactly 3000.
	State.mu.Lock()
	u.TrafficUsedBytes = 8000
	State.mu.Unlock()
	sampleUsageHistory()
	today := tehranToday()
	if u.UsageHistory[today] != 3000 {
		t.Fatalf("today bucket = %d, want 3000 (all history %v)", u.UsageHistory[today], u.UsageHistory)
	}
	series := usageHistorySeries(u.UsageHistory, 14)
	if len(series) != 14 {
		t.Fatalf("series len = %d, want 14", len(series))
	}
	last := series[13]
	if last["date"] != today || last["bytes"].(int64) != 3000 {
		t.Fatalf("series tail = %v, want today/3000", last)
	}
}

func TestThrottleRespectsLimit(t *testing.T) {
	State.mu.Lock()
	oldUsers := State.Users
	State.Users = map[string]*User{}
	u := &User{UserID: "tj1", Username: "slow", ConfigUUID: "tj-uuid", Status: "active", SpeedLimitMbps: 8} // 1 MB/s
	State.Users[u.UserID] = u
	State.mu.Unlock()
	defer func() {
		State.mu.Lock()
		State.Users = oldUsers
		State.mu.Unlock()
		rateBuckets.Delete("tj-uuid")
	}()

	throttleUser("tj-uuid", 500_000) // half the burst, should be instant
	start := time.Now()
	throttleUser("tj-uuid", 2_000_000) // 2MB at 1MB/s with 0.5MB left -> ~1.5s wait (capped 3s)
	elapsed := time.Since(start)
	if elapsed < 900*time.Millisecond {
		t.Fatalf("throttle too fast: %v (limit not enforced)", elapsed)
	}
	if elapsed > 3*time.Second {
		t.Fatalf("throttle too slow: %v", elapsed)
	}
	// unlimited user passes instantly
	start = time.Now()
	throttleUser("nobody-uuid", 10_000_000)
	if time.Since(start) > 200*time.Millisecond {
		t.Fatal("unlimited user was throttled")
	}
	_ = strings.TrimSpace // keep strings import if unused elsewhere
}

func TestIPLimitSemantics(t *testing.T) {
	const id = "zz-ip-test-uuid"
	userIPTrackerMu.Lock()
	delete(userIPTracker, id)
	userIPTrackerMu.Unlock()
	defer func() {
		userIPTrackerMu.Lock()
		delete(userIPTracker, id)
		userIPTrackerMu.Unlock()
	}()

	// userIPLimit: 0/unset = unlimited, positive = that cap.
	if got := userIPLimit(&User{ConcurrentConnections: 0}); got != 0 {
		t.Fatalf("userIPLimit(0) = %d, want 0 (unlimited)", got)
	}
	if got := userIPLimit(&User{ConcurrentConnections: 2}); got != 2 {
		t.Fatalf("userIPLimit(2) = %d, want 2", got)
	}
	if got := userIPLimit(nil); got != 0 {
		t.Fatalf("userIPLimit(nil) = %d, want 0", got)
	}

	// Cap of 1: first IP admitted, its reconnects admitted, second IP cut.
	if !recordUserIP(id, "10.0.0.1", 1) {
		t.Fatal("first IP must be admitted")
	}
	if !recordUserIP(id, "10.0.0.1", 1) {
		t.Fatal("same IP reconnect must be admitted")
	}
	if recordUserIP(id, "10.0.0.2", 1) {
		t.Fatal("second IP beyond cap 1 must be rejected")
	}
	// Unlimited (0): any number of IPs admitted.
	if !recordUserIP(id, "10.0.0.2", 0) {
		t.Fatal("unlimited must admit a new IP")
	}
	if !recordUserIP(id, "10.0.0.3", 0) {
		t.Fatal("unlimited must admit another new IP")
	}
	// touchUserIP keeps an admitted IP fresh and ignores unknown ones.
	touchUserIP(id, "10.0.0.1")
	touchUserIP(id, "10.9.9.9")
	if got := len(userCurrentIPs(id)); got != 3 {
		t.Fatalf("tracked IPs = %d, want 3", got)
	}
}
