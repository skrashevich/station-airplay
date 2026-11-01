package pcmbundle

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLibraryABIUsesELFClassAndMachine(t *testing.T) {
	for _, arch := range []string{"armv7", "arm64"} {
		path := filepath.Join(t.TempDir(), "libasound.so.2")
		os.WriteFile(path, elfFixture(arch), 0o600)
		got, err := libraryABI(path)
		if err != nil || got != arch {
			t.Fatalf("got %s %v", got, err)
		}
	}
	data := elfFixture("arm64")
	binary.LittleEndian.PutUint16(data[18:], 62)
	path := filepath.Join(t.TempDir(), "wrong-machine")
	os.WriteFile(path, data, 0o600)
	if _, err := libraryABI(path); err == nil {
		t.Fatal("selected incompatible native library")
	}
}
func TestChildEnvironmentPreservesExplicitOverridesWithoutMutatingProcess(t *testing.T) {
	t.Setenv("STATION_PCM_LIBRARY", "/original/libasound.so.2")
	t.Setenv("LD_LIBRARY_PATH", "/custom/lib")
	t.Setenv("ALSA_CONFIG_PATH", "/custom/alsa.conf")
	t.Setenv("ALSA_CONFIG_DIR", "/custom/alsa")
	env := childEnvironment("/chosen/lib/libasound.so.2")
	joined := "\n" + strings.Join(env, "\n") + "\n"
	if !strings.Contains(joined, "\nSTATION_PCM_LIBRARY=/chosen/lib/libasound.so.2\n") || !strings.Contains(joined, "\nLD_LIBRARY_PATH=/chosen/lib:/custom/lib\n") {
		t.Fatal(env)
	}
	for _, item := range env {
		if strings.HasPrefix(item, "ALSA_CONFIG_") {
			t.Fatalf("explicit ALSA override replaced: %s", item)
		}
	}
	if os.Getenv("STATION_PCM_LIBRARY") != "/original/libasound.so.2" || os.Getenv("LD_LIBRARY_PATH") != "/custom/lib" {
		t.Fatal("process environment changed")
	}
}
