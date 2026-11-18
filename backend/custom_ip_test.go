package main

import (
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The create-user switches «افزودن ۱۰ کانفیگ با IPهای اسکن‌شده» store
// User.CustomIPInbounds, but until 2026-11-18 nothing read that field, so
// the option could never add a config. These tests pin the wired-up
// behavior: the main config comes first and byte-identical, then one
// variant per saved scanned IP (max 10) with only the address swapped.
func TestCustomIPVariants(t *testing.T) {
	State.mu.Lock()
	oldUsers, oldInbounds, oldSettings, oldDir := State.Users, State.Inbounds, State.Settings, State.DataDir
	State.Users = map[string]*User{}
	State.Inbounds = map[string]*Inbound{
		"default": {InboundID: "default", Name: "Default", Protocol: "vless", Network: "ws", Security: "tls", Port: 443},
	}
	State.Settings = map[string]any{"domain": "example.com", "config_name_prefix": ""}
	State.DataDir = t.TempDir()
	State.mu.Unlock()
	defer func() {
		State.mu.Lock()
		State.Users, State.Inbounds, State.Settings, State.DataDir = oldUsers, oldInbounds, oldSettings, oldDir
		State.mu.Unlock()
	}()

	// Three usable IPs, one junk line the generator must skip.
	ips := "104.21.5.3\nnot-an-ip\n172.67.9.1\n1.1.1.1\n"
	if err := os.WriteFile(filepath.Join(State.DataDir, "scanned_cf.txt"), []byte(ips), 0644); err != nil {
		t.Fatal(err)
	}

	r := httptest.NewRequest("GET", "https://example.com/sub/x", nil)

	flagged := &User{UserID: "cu1", Username: "parham", ConfigUUID: "11111111-2222-3333-4444-555555555555", Status: "active",
		InboundIDs:       []string{"default"},
		CustomIPInbounds: map[string][]string{"cf": {"default"}, "railway": {"default"}}}
	State.mu.Lock()
	State.Users[flagged.UserID] = flagged
	State.mu.Unlock()

	cfgs := generateUserConfigs(r, flagged)
	if len(cfgs) != 4 {
		t.Fatalf("expected main config + 3 CF variants (no railway file saved), got %d: %v", len(cfgs), cfgs)
	}
	main := generateUserConfig(r, flagged, "default")
	if cfgs[0] != main {
		t.Fatalf("main config changed:\n got %s\nwant %s", cfgs[0], main)
	}
	for i, ip := range []string{"104.21.5.3", "172.67.9.1", "1.1.1.1"} {
		v := cfgs[i+1]
		if !strings.Contains(v, "@"+ip+":443") {
			t.Fatalf("variant %d does not carry scanned IP %s: %s", i, ip, v)
		}
		if !strings.Contains(v, "host=example.com") {
			t.Fatalf("variant %d lost the domain host parameter: %s", i, v)
		}
		h := strings.LastIndex(v, "#")
		name, err := url.QueryUnescape(v[h+1:])
		if err != nil || name != "parham · "+ip {
			t.Fatalf("variant %d name = %q (err %v), want %q", i, name, err, "parham · "+ip)
		}
	}

	// A user without the switch gets exactly the single main config.
	plain := &User{UserID: "cu2", Username: "parham", ConfigUUID: "11111111-2222-3333-4444-555555555555", Status: "active",
		InboundIDs: []string{"default"}}
	if got := generateUserConfigs(r, plain); len(got) != 1 || got[0] != main {
		t.Fatalf("unflagged user: got %v, want exactly the main config", got)
	}
}

// The address swap itself: only the authority host changes, everything
// else (uuid, port, query, name base) is preserved; unusable inputs are
// rejected.
func TestConfigWithScannedIP(t *testing.T) {
	cfg := "vless://11111111-2222-3333-4444-555555555555@example.com:443?encryption=none&host=example.com&security=tls#parham"
	got := configWithScannedIP(cfg, "104.21.5.3")
	want := "vless://11111111-2222-3333-4444-555555555555@104.21.5.3:443?encryption=none&host=example.com&security=tls#parham%20%C2%B7%20104.21.5.3"
	if got != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
	if v := configWithScannedIP(cfg, "not-an-ip"); v != "" {
		t.Fatalf("junk IP accepted: %s", v)
	}
	if v := configWithScannedIP("https://t.me/proxy?server=example.com&port=443&secret=<redacted>", "1.1.1.1"); v != "" {
		t.Fatalf("t.me proxy link must not get an IP variant: %s", v)
	}
	// An ip:port scanner entry keeps only the IP; the config's port wins.
	if v := configWithScannedIP(cfg, "104.21.5.3:8443"); !strings.Contains(v, "@104.21.5.3:443") {
		t.Fatalf("ip:port entry mishandled: %s", v)
	}
}
