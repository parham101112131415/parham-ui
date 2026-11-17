package main

import (
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestGitBlobSHA(t *testing.T) {
	// The empty blob hash is a fixed git constant.
	if got := gitBlobSHA(nil); got != "e69de29bb2d1d6434b8b29ae775ad8c2e48c5391" {
		t.Fatalf("empty blob sha = %s", got)
	}
	// `echo "hello world" | git hash-object --stdin` from Pro Git.
	if got := gitBlobSHA([]byte("hello world\n")); got != "3b18e512dba79e4c8300dd08aeb37f8e728b8dad" {
		t.Fatalf("hello world blob sha = %s", got)
	}
}

func TestParseGitHubFileURL(t *testing.T) {
	cases := []struct {
		in                 string
		repo, branch, path string
		ok                 bool
	}{
		{"https://raw.githubusercontent.com/parham101112131415/parham-panel-backup/main/parham-backup.json",
			"parham101112131415/parham-panel-backup", "main", "parham-backup.json", true},
		{"https://github.com/parham101112131415/parham-panel-backup/blob/main/parham-backup.json",
			"parham101112131415/parham-panel-backup", "main", "parham-backup.json", true},
		{"https://github.com/parham101112131415/parham-panel-backup/raw/main/dir/parham-backup.json",
			"parham101112131415/parham-panel-backup", "main", "dir/parham-backup.json", true},
		{"https://github.com/parham101112131415/parham-panel-backup", "", "", "", false},
		{"https://example.com/some/backup.json", "", "", "", false},
		{"not a url", "", "", "", false},
	}
	for _, c := range cases {
		repo, branch, path, ok := parseGitHubFileURL(c.in)
		if ok != c.ok || repo != c.repo || branch != c.branch || path != c.path {
			t.Errorf("parseGitHubFileURL(%q) = (%q, %q, %q, %v), want (%q, %q, %q, %v)",
				c.in, repo, branch, path, ok, c.repo, c.branch, c.path, c.ok)
		}
	}
}

func TestBackupRestoreRoundTrip(t *testing.T) {
	State.mu.Lock()
	oldUsers, oldInbounds, oldNodes := State.Users, State.Inbounds, State.Nodes
	oldPlans, oldSettings, oldPassword, oldDir := State.BotPlans, State.Settings, State.Password, State.DataDir
	State.Users = map[string]*User{
		"u1": {UserID: "u1", Username: "roundtrip", ConfigUUID: "11111111-2222-3333-4444-555555555555", Status: "active"},
	}
	State.Settings = map[string]any{"marker": "gh-backup-test"}
	State.DataDir = t.TempDir()
	State.mu.Unlock()
	defer func() {
		State.mu.Lock()
		State.Users, State.Inbounds, State.Nodes = oldUsers, oldInbounds, oldNodes
		State.BotPlans, State.Settings, State.Password, State.DataDir = oldPlans, oldSettings, oldPassword, oldDir
		State.mu.Unlock()
	}()

	data, err := buildBackupBytes()
	if err != nil {
		t.Fatalf("buildBackupBytes: %v", err)
	}
	if !strings.Contains(string(data), "roundtrip") {
		t.Fatalf("backup payload is missing the test user")
	}

	// Wipe the user, then restore from the backup bytes.
	State.mu.Lock()
	State.Users = map[string]*User{}
	State.Settings = map[string]any{}
	State.mu.Unlock()

	status, payload := restoreBackupBytes(data)
	if status != 200 || payload["ok"] != true {
		t.Fatalf("restore status=%d payload=%v", status, payload)
	}
	State.mu.RLock()
	u := State.Users["u1"]
	marker := State.Settings["marker"]
	State.mu.RUnlock()
	if u == nil || u.Username != "roundtrip" || u.ConfigUUID != "11111111-2222-3333-4444-555555555555" {
		t.Fatalf("user not restored: %+v", u)
	}
	if marker != "gh-backup-test" {
		t.Fatalf("settings not restored: %v", marker)
	}

	// Garbage input must be rejected, not applied.
	if status, _ := restoreBackupBytes([]byte("{nope")); status == 200 {
		t.Fatalf("invalid JSON restore should fail")
	}
}

// The migration guarantee parham asked for: after a backup → import cycle
// on a fresh deployment, the username-form subscription link
// (/sub/<username>) resolves, and the user's used traffic and expiry come
// back EXACTLY as they were — never reset.
func TestSubByUsernameAndUsagePreserved(t *testing.T) {
	const usedBytes = int64(197 * 1024 * 1024) // his example: 197 MB used
	const expireAt = "2027-06-01T00:00:00Z"
	const cfgUUID = "923e2bed-b2eb-8d0f-7cd8-9d3c9f5bb7d6"

	State.mu.Lock()
	oldUsers, oldInbounds, oldNodes := State.Users, State.Inbounds, State.Nodes
	oldPlans, oldSettings, oldPassword, oldDir := State.BotPlans, State.Settings, State.Password, State.DataDir
	State.Users = map[string]*User{
		"u1": {
			UserID: "u1", Username: "parhamtest", ConfigUUID: cfgUUID, Status: "active",
			TrafficUsedBytes: usedBytes, TrafficLimitBytes: 0, ExpireAt: expireAt,
			UsageHistory:      map[string]int64{"2026-11-17": 1048576},
			UsageSampledBytes: usedBytes,
		},
	}
	State.Settings = map[string]any{}
	State.DataDir = t.TempDir()
	State.mu.Unlock()
	defer func() {
		State.mu.Lock()
		State.Users, State.Inbounds, State.Nodes = oldUsers, oldInbounds, oldNodes
		State.BotPlans, State.Settings, State.Password, State.DataDir = oldPlans, oldSettings, oldPassword, oldDir
		State.mu.Unlock()
	}()

	data, err := buildBackupBytes()
	if err != nil {
		t.Fatalf("buildBackupBytes: %v", err)
	}

	// Simulate the fresh deployment: nothing exists, then the backup lands.
	State.mu.Lock()
	State.Users = map[string]*User{}
	State.mu.Unlock()
	if status, payload := restoreBackupBytes(data); status != 200 || payload["ok"] != true {
		t.Fatalf("restore status=%d payload=%v", status, payload)
	}

	State.mu.RLock()
	u := State.Users["u1"]
	State.mu.RUnlock()
	if u == nil {
		t.Fatalf("user missing after restore")
	}
	if u.TrafficUsedBytes != usedBytes {
		t.Fatalf("used traffic reset or changed: got %d, want %d", u.TrafficUsedBytes, usedBytes)
	}
	if u.TrafficLimitBytes != 0 || u.ExpireAt != expireAt {
		t.Fatalf("limit/expiry changed: limit=%d expire=%q", u.TrafficLimitBytes, u.ExpireAt)
	}
	if u.UsageHistory["2026-11-17"] != 1048576 || u.UsageSampledBytes != usedBytes {
		t.Fatalf("usage history changed: %v sampled=%d", u.UsageHistory, u.UsageSampledBytes)
	}

	expT, _ := time.Parse(time.RFC3339, expireAt)
	wantInfo := fmt.Sprintf("upload=0; download=%d; total=%d; expire=%d", usedBytes, int64(0), expT.Unix())

	// The username-form link must serve the subscription with the exact
	// same usage/expiry figures; the old UUID form must keep working too.
	for _, id := range []string{"parhamtest", cfgUUID} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("GET", "/sub/"+id, nil)
		handleSubPlainText(rec, req, id)
		if rec.Code != 200 {
			t.Fatalf("/sub/%s status = %d, want 200", id, rec.Code)
		}
		if got := rec.Header().Get("Subscription-Userinfo"); got != wantInfo {
			t.Fatalf("/sub/%s userinfo = %q, want %q", id, got, wantInfo)
		}
	}
}
