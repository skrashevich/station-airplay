package pcmbundle

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/binary"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func elfFixture(arch string) []byte {
	b := make([]byte, 64)
	copy(b, "\x7fELF")
	b[4] = 1
	b[5] = 1
	machine := uint16(40)
	if arch == "arm64" {
		b[4] = 2
		machine = 183
	}
	binary.LittleEndian.PutUint16(b[18:], machine)
	return append(b, []byte("PCM helper "+arch)...)
}
func bundleFixture(t *testing.T) (string, []byte, map[string][]byte) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "receiver")
	host := elfFixture("arm64")
	if err := os.WriteFile(path, host, 0o751); err != nil {
		t.Fatal(err)
	}
	helpers := map[string][]byte{"armv7": elfFixture("armv7"), "arm64": elfFixture("arm64")}
	if err := Append(t.Context(), path, helpers); err != nil {
		t.Fatal(err)
	}
	return path, host, helpers
}
func fixtureSelect(arch string) func() (string, []string, error) {
	return func() (string, []string, error) {
		return arch, []string{"STATION_PCM_LIBRARY=/test/libasound.so.2"}, nil
	}
}
func TestAppendKeepsOneExecutableAndReplacesPreviousPayload(t *testing.T) {
	path, host, helpers := bundleFixture(t)
	first, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first[:len(host)], host) {
		t.Fatal("host ELF changed")
	}
	if err := Append(t.Context(), path, helpers); err != nil {
		t.Fatal(err)
	}
	second, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(second) != len(first) {
		t.Fatalf("rebundling grew file: %d -> %d", len(first), len(second))
	}
	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0o751 {
		t.Fatal("host mode changed")
	}
	entries, _ := os.ReadDir(filepath.Dir(path))
	if len(entries) != 1 {
		t.Fatal("adjacent PCM sidecars created")
	}
}
func TestResolveSelectsOneABIAndRepairsPrivateCache(t *testing.T) {
	path, _, helpers := bundleFixture(t)
	state := filepath.Join(t.TempDir(), "state")
	command, env, err := resolve(t.Context(), path, state, fixtureSelect("armv7"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(env, " ") != "STATION_PCM_LIBRARY=/test/libasound.so.2" {
		t.Fatal(env)
	}
	data, err := os.ReadFile(command)
	if err != nil || !bytes.Equal(data, helpers["armv7"]) {
		t.Fatalf("wrong ABI: %v", err)
	}
	for _, dir := range []string{filepath.Join(state, "pcm"), filepath.Dir(command)} {
		info, err := os.Stat(dir)
		if err != nil || info.Mode().Perm() != 0o700 {
			t.Fatalf("cache is not private: %s %v", dir, err)
		}
	}
	if err := os.WriteFile(command, []byte("corrupt"), 0o700); err != nil {
		t.Fatal(err)
	}
	repaired, _, err := resolve(t.Context(), path, state, fixtureSelect("armv7"))
	if err != nil || repaired != command {
		t.Fatalf("repair failed: %v", err)
	}
	data, _ = os.ReadFile(command)
	if !bytes.Equal(data, helpers["armv7"]) {
		t.Fatal("cache was not repaired")
	}
	if err := os.Remove(command); err != nil {
		t.Fatal(err)
	}
	if _, _, err := resolve(t.Context(), path, state, fixtureSelect("armv7")); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(filepath.Join(state, "pcm"))
	if len(entries) != 1 {
		t.Fatal("unselected helper extracted")
	}
}
func TestResolveConcurrentCachePublication(t *testing.T) {
	path, _, _ := bundleFixture(t)
	state := t.TempDir()
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			if _, _, err := resolve(t.Context(), path, state, fixtureSelect("arm64")); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
}
func TestResolveRejectsMissingAndCorruptPayload(t *testing.T) {
	path := filepath.Join(t.TempDir(), "plain")
	os.WriteFile(path, elfFixture("arm64"), 0o700)
	if _, _, err := resolve(t.Context(), path, t.TempDir(), fixtureSelect("arm64")); err == nil || !strings.Contains(err.Error(), "payload") {
		t.Fatalf("plain build accepted: %v", err)
	}
	path, host, _ := bundleFixture(t)
	original, _ := os.ReadFile(path)
	reader, err := zip.NewReader(bytes.NewReader(original), int64(len(original)))
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	output.Write(host)
	writer := zip.NewWriter(&output)
	writer.SetComment(reader.Comment)
	for _, entry := range reader.File {
		input, err := entry.Open()
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(input)
		input.Close()
		if err != nil {
			t.Fatal(err)
		}
		if entry.Name == helperEntry("armv7") {
			data[len(data)-1] ^= 1
		}
		destination, err := writer.Create(entry.Name)
		if err != nil {
			t.Fatal(err)
		}
		destination.Write(data)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(path, output.Bytes(), 0o700)
	if _, _, err := resolve(t.Context(), path, t.TempDir(), fixtureSelect("armv7")); err == nil || !strings.Contains(err.Error(), "SHA-256") {
		t.Fatalf("corrupt helper accepted: %v", err)
	}
}
func TestAppendHandlesShiftedZIPOverlayWithoutTruncatingHost(t *testing.T) {
	path, host, helpers := bundleFixture(t)
	original, _ := os.ReadFile(path)
	prefix := append(bytes.Clone(host), bytes.Repeat([]byte{0x42}, 31)...)
	shifted := append(bytes.Clone(prefix), original[len(host):]...)
	os.WriteFile(path, shifted, 0o700)
	if err := Append(t.Context(), path, helpers); err != nil {
		t.Fatal(err)
	}
	final, _ := os.ReadFile(path)
	if !bytes.Equal(final[:len(prefix)], prefix) {
		t.Fatal("host prefix truncated while replacing shifted overlay")
	}
	if _, _, err := resolve(t.Context(), path, t.TempDir(), fixtureSelect("armv7")); err != nil {
		t.Fatal(err)
	}
}
func TestCancellationLeavesHostAndCacheUntouched(t *testing.T) {
	path, _, helpers := bundleFixture(t)
	original, _ := os.ReadFile(path)
	state := filepath.Join(t.TempDir(), "state")
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := Append(ctx, path, helpers); err == nil {
		t.Fatal("canceled append succeeded")
	}
	final, _ := os.ReadFile(path)
	if !bytes.Equal(final, original) {
		t.Fatal("canceled append changed host")
	}
	if _, _, err := resolve(ctx, path, state, fixtureSelect("armv7")); err == nil {
		t.Fatal("canceled resolve succeeded")
	}
	if _, err := os.Stat(state); !os.IsNotExist(err) {
		t.Fatal("canceled resolve created cache")
	}
}

func TestStripPreservesHostPrefixAndIsIdempotent(t *testing.T) {
	for _, packed := range []bool{false, true} {
		t.Run(map[bool]string{false: "raw", true: "packed-prefix"}[packed], func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "receiver")
			host := elfFixture("arm64")
			if packed {
				host = append(host, []byte("compressed-host-footer-UPX!")...)
			}
			if err := os.WriteFile(path, host, 0o751); err != nil {
				t.Fatal(err)
			}
			if err := Append(t.Context(), path, map[string][]byte{"armv7": elfFixture("armv7")}); err != nil {
				t.Fatal(err)
			}
			stripped, err := Strip(t.Context(), path)
			if err != nil || !stripped {
				t.Fatalf("Strip: %v %v", stripped, err)
			}
			actual, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(actual, host) {
				t.Fatalf("host changed: %v", err)
			}
			info, _ := os.Stat(path)
			if info.Mode().Perm() != 0o751 {
				t.Fatal("strip changed host mode")
			}
			if stripped, err = Strip(t.Context(), path); err != nil || stripped {
				t.Fatalf("second Strip: %v %v", stripped, err)
			}
		})
	}
}
func TestOwnedZIPWithTrailingBytesIsRejectedWithoutChangingHost(t *testing.T) {
	path, _, helpers := bundleFixture(t)
	before, _ := os.ReadFile(path)
	before = append(before, []byte("UPX!trailer-metadata-after-overlay")...)
	if err := os.WriteFile(path, before, 0o751); err != nil {
		t.Fatal(err)
	}
	if err := Append(t.Context(), path, helpers); err == nil || !strings.Contains(err.Error(), "compress before bundling") {
		t.Fatalf("trailer accepted by Append: %v", err)
	}
	if removed, err := Strip(t.Context(), path); err == nil || removed || !strings.Contains(err.Error(), "compress before bundling") {
		t.Fatalf("trailer accepted by Strip: %v %v", removed, err)
	}
	after, _ := os.ReadFile(path)
	if !bytes.Equal(after, before) {
		t.Fatal("rejected trailer changed original executable")
	}
}
