package main

import "testing"

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
