package main

import (
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// The multi-config bundle was reverted at the owner's request: a state
// polluted by the bundle build (seeded inbounds attached to the user)
// must clean itself up and go back to exactly one config named exactly
// after the username.
func TestBundleRevertRestoresSingleConfig(t *testing.T) {
	State.mu.Lock()
	oldUsers, oldInbounds, oldSettings := State.Users, State.Inbounds, State.Settings
	State.Users = map[string]*User{}
	State.Inbounds = map[string]*Inbound{
		"default":             {InboundID: "default", Name: "Default", Protocol: "vless", Network: "ws", Security: "tls", Port: 443},
		"bundle-vless-ws":     {InboundID: "bundle-vless-ws", Protocol: "vless", Network: "ws", Security: "tls", Port: 443},
		"bundle-vless-xhttp":  {InboundID: "bundle-vless-xhttp", Protocol: "vless", Network: "xhttp", Security: "tls", Port: 443},
		"bundle-vless-reality": {InboundID: "bundle-vless-reality", Protocol: "reality", Network: "tcp", Security: "reality", Port: 8443},
		"bundle-shadowsocks":  {InboundID: "bundle-shadowsocks", Protocol: "shadowsocks", Port: 8388},
	}
	State.Settings = map[string]any{"domain": "example.com", "config_name_prefix": "", "bundle_seeded": true}
	u := &User{UserID: "bu1", Username: "parham", ConfigUUID: "11111111-2222-3333-4444-555555555555", Status: "active",
		InboundIDs: []string{"default", "bundle-vless-ws", "bundle-vless-xhttp", "bundle-vless-reality", "bundle-shadowsocks"}}
	State.Users[u.UserID] = u
	State.mu.Unlock()
	defer func() {
		State.mu.Lock()
		State.Users, State.Inbounds, State.Settings = oldUsers, oldInbounds, oldSettings
		State.mu.Unlock()
	}()

	r := httptest.NewRequest("GET", "https://example.com/sub/x", nil)
	cfgs := generateUserConfigs(r, u)
	if len(cfgs) != 1 {
		t.Fatalf("expected exactly 1 config after revert, got %d: %v", len(cfgs), cfgs)
	}
	i := strings.Index(cfgs[0], "#")
	if i < 0 {
		t.Fatalf("config has no name fragment: %s", cfgs[0])
	}
	name, err := url.QueryUnescape(cfgs[0][i+1:])
	if err != nil || name != "parham" {
		t.Fatalf("config name = %q (err %v), want exactly \"parham\"", name, err)
	}
	State.mu.RLock()
	defer State.mu.RUnlock()
	for _, bid := range bundleInboundIDs {
		if _, ok := State.Inbounds[bid]; ok {
			t.Fatalf("bundle inbound %s still in state", bid)
		}
	}
	if len(u.InboundIDs) != 1 || u.InboundIDs[0] != "default" {
		t.Fatalf("user inbound_ids = %v, want [default]", u.InboundIDs)
	}
}
