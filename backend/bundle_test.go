package main

import (
	"encoding/json"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestBundleProducesRealMultiProtocolConfigs(t *testing.T) {
	State.mu.Lock()
	oldUsers, oldInbounds, oldSettings := State.Users, State.Inbounds, State.Settings
	State.Users = map[string]*User{}
	State.Inbounds = map[string]*Inbound{}
	State.Settings = map[string]any{"domain": "example.com", "config_name_prefix": ""}
	u := &User{UserID: "bu1", Username: "parham", ConfigUUID: "11111111-2222-3333-4444-555555555555", Status: "active"}
	State.Users[u.UserID] = u
	State.mu.Unlock()
	defer func() {
		State.mu.Lock()
		State.Users, State.Inbounds, State.Settings = oldUsers, oldInbounds, oldSettings
		State.mu.Unlock()
	}()

	ensureBundleInbounds()

	r := httptest.NewRequest("GET", "https://example.com/sub/x", nil)
	cfgs := generateUserConfigs(r, u)
	if len(cfgs) < 4 {
		t.Fatalf("expected >=4 bundle configs, got %d: %v", len(cfgs), cfgs)
	}
	joined := strings.Join(cfgs, "\n")
	for _, want := range []string{
		"vless://", "ss://",
		"example.com:8443", "example.com:8388",
		"path=%2Fws%2F11111111-2222-3333-4444-555555555555",
		"path=%2Fxhttp-siz10%2Fstream-up%2F11111111-2222-3333-4444-555555555555",
		"security=reality", "pbk=",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("bundle configs missing %q\n---\n%s", want, joined)
		}
	}
	// names (URL-unescaped fragments) must carry the per-type labels, all distinct
	names := map[string]bool{}
	for _, c := range cfgs {
		if i := strings.Index(c, "#"); i >= 0 {
			if dec, err := url.QueryUnescape(c[i+1:]); err == nil {
				names[dec] = true
			}
		}
	}
	for _, want := range []string{
		"parham | VLESS · WS · TLS",
		"parham | VLESS · XHTTP · TLS",
		"parham | VLESS · Reality",
		"parham | Shadowsocks",
	} {
		if !names[want] {
			t.Fatalf("missing config name %q in %v", want, names)
		}
	}
	if len(names) != len(cfgs) {
		t.Fatalf("config names not all distinct: %v", names)
	}
	// The Xray side must really serve the direct-port bundle inbounds:
	// reality :8443 carrying this user as a client, shadowsocks :8388
	// carrying this user's own password.
	raw, err := generateXrayConfig()
	if err != nil {
		t.Fatalf("generateXrayConfig: %v", err)
	}
	var xc struct {
		Inbounds []struct {
			Port     int            `json:"port"`
			Protocol string         `json:"protocol"`
			Settings map[string]any `json:"settings"`
		} `json:"inbounds"`
	}
	if err := json.Unmarshal(raw, &xc); err != nil {
		t.Fatalf("xray config JSON: %v", err)
	}
	var sawReality, sawSS bool
	for _, ib := range xc.Inbounds {
		if ib.Port == 8443 && ib.Protocol == "vless" {
			if cl, ok := ib.Settings["clients"].([]any); ok && len(cl) > 0 {
				if m, ok := cl[0].(map[string]any); ok && m["id"] == u.ConfigUUID {
					sawReality = true
				}
			}
		}
		if ib.Port == 8388 && ib.Protocol == "shadowsocks" {
			if us, ok := ib.Settings["users"].([]any); ok {
				for _, it := range us {
					if m, ok := it.(map[string]any); ok && m["password"] == u.ShadowsocksPassword && u.ShadowsocksPassword != "" {
						sawSS = true
					}
				}
			}
		}
	}
	if !sawReality {
		t.Fatal("xray config has no reality :8443 inbound with the user as client")
	}
	if !sawSS {
		t.Fatal("xray config has no shadowsocks :8388 inbound with the user password")
	}
	t.Logf("configs:\n%s", joined)
}
