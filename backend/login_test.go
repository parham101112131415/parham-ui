package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// A state file created before the rebrand salt change (fc5d40f) stores
// hashPasswordLegacy("admin"); the panel must still accept "admin" for it
// and flag the match as legacy so the stored hash gets upgraded.
func TestPasswordMatchesLegacySalt(t *testing.T) {
	stored := hashPasswordLegacy("admin")
	ok, legacy := passwordMatches("admin", stored)
	if !ok || !legacy {
		t.Fatalf("legacy hash: ok=%v legacy=%v, want true,true", ok, legacy)
	}
	if ok, _ := passwordMatches("wrong", stored); ok {
		t.Fatalf("wrong password matched legacy hash")
	}
}

// The break-glass reset: with PARHAM_RESET_PASSWORD set, startup overwrites
// a stored hash that matches nothing (the lockout scenario) and persists the
// new hash, so login with the reset value works afterwards.
func TestEnvPasswordReset(t *testing.T) {
	oldDir, oldPwd := State.DataDir, State.Password
	defer func() { State.DataDir, State.Password = oldDir, oldPwd }()

	State.DataDir = t.TempDir()
	State.Password = "a-stored-hash-that-matches-nothing"
	t.Setenv("PARHAM_RESET_PASSWORD", "fresh-pass-123")

	applyEnvPasswordReset()

	if ok, _ := passwordMatches("fresh-pass-123", State.Password); !ok {
		t.Fatalf("reset password does not match stored hash after reset")
	}
	raw, err := os.ReadFile(filepath.Join(State.DataDir, "parham_state.json"))
	if err != nil {
		t.Fatalf("state file not saved: %v", err)
	}
	var disk struct {
		Password string `json:"password_hash"`
	}
	if err := json.Unmarshal(raw, &disk); err != nil {
		t.Fatalf("state file unreadable: %v", err)
	}
	if disk.Password != hashPassword("fresh-pass-123") {
		t.Fatalf("persisted hash = %q, want hash of the reset password", disk.Password)
	}

	// Without the variable set, the reset must be a no-op.
	t.Setenv("PARHAM_RESET_PASSWORD", "")
	before := State.Password
	applyEnvPasswordReset()
	if State.Password != before {
		t.Fatalf("reset ran with empty PARHAM_RESET_PASSWORD")
	}
}

func TestPasswordMatchesCurrentSalt(t *testing.T) {
	stored := hashPassword("admin")
	ok, legacy := passwordMatches("admin", stored)
	if !ok || legacy {
		t.Fatalf("current hash: ok=%v legacy=%v, want true,false", ok, legacy)
	}
	if ok, _ := passwordMatches("admin ", stored); ok {
		t.Fatalf("password with trailing space must not match")
	}
}
