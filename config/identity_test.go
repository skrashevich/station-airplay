package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestIdentitySurvivesRestartAndCorruptionIsNotOverwritten(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "state")
	a, err := LoadIdentity(dir)
	if err != nil {
		t.Fatal(err)
	}
	b, err := LoadIdentity(dir)
	if err != nil || a != b {
		t.Fatalf("identity changed: %v", err)
	}
	info, _ := os.Stat(filepath.Join(dir, "identity.json"))
	if info.Mode().Perm() != 0600 {
		t.Fatal("identity is not private")
	}
	os.WriteFile(filepath.Join(dir, "identity.json"), []byte("broken"), 0600)
	if _, err := LoadIdentity(dir); err == nil {
		t.Fatal("corrupted identity silently reset")
	}
}
