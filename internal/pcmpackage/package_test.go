package pcmpackage

import (
	"archive/zip"
	"bytes"
	"encoding/binary"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

func elfFixture(t *testing.T, arch string) string {
	t.Helper()
	header := make([]byte, 64)
	copy(header, "\x7fELF")
	header[5], header[6] = 1, 1
	binary.LittleEndian.PutUint16(header[16:], 2)
	binary.LittleEndian.PutUint32(header[20:], 1)
	binary.LittleEndian.PutUint64(header[24:], 0x18000)
	if arch == "armv7" {
		header[4] = 1
		binary.LittleEndian.PutUint16(header[18:], 40)
	} else {
		header[4] = 2
		binary.LittleEndian.PutUint16(header[18:], 183)
	}
	path := filepath.Join(t.TempDir(), arch)
	if err := os.WriteFile(path, header, 0o750); err != nil {
		t.Fatal(err)
	}
	return path
}

func bundleHelpers(t *testing.T, path string, expected map[string]string) {
	t.Helper()
	archive, err := zip.OpenReader(path)
	if err != nil {
		t.Fatal(err)
	}
	defer archive.Close()
	seen := make(map[string]bool)
	for _, file := range archive.File {
		r, err := file.Open()
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(r)
		r.Close()
		if err != nil {
			t.Fatal(err)
		}
		matched := false
		for arch, path := range expected {
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if bytes.Equal(data, want) {
				seen[arch] = true
				matched = true
			}
		}
		if bytes.HasPrefix(data, []byte("\x7fELF")) && !matched {
			t.Fatalf("unexpected native payload %s", file.Name)
		}
	}
	if len(seen) != len(expected) {
		t.Fatalf("bundle helpers %v, expected %v", seen, expected)
	}
}

func TestBuildProducesOneExecutableAndPreservesInput(t *testing.T) {
	for _, arch := range []string{"armv7", "arm64"} {
		t.Run(arch, func(t *testing.T) {
			// Prebuilt payloads need no Go installation in package/test containers.
			t.Setenv("PATH", t.TempDir())
			input := elfFixture(t, arch)
			original, _ := os.ReadFile(input)
			output := filepath.Join(t.TempDir(), "receiver")
			options := Options{Arch: arch, Binary: input, Output: output, Armv7Binary: elfFixture(t, "armv7"), Arm64Binary: elfFixture(t, "arm64")}
			if err := Build(t.Context(), options); err != nil {
				t.Fatal(err)
			}
			got, err := os.ReadFile(output)
			if err != nil || len(got) <= len(original) || !bytes.Equal(got[:len(original)], original) {
				t.Fatalf("original ELF/entry changed: %v", err)
			}
			inputAfter, _ := os.ReadFile(input)
			if !bytes.Equal(inputAfter, original) {
				t.Fatal("input was modified despite separate output")
			}
			files, err := os.ReadDir(filepath.Dir(output))
			if err != nil || len(files) != 1 || files[0].Name() != "receiver" {
				t.Fatalf("extra installed files: %v %v", files, err)
			}
			info, _ := os.Stat(output)
			if info.Mode().Perm() != 0o750 {
				t.Fatalf("executable mode changed: %v", info.Mode())
			}
			expected := map[string]string{"armv7": options.Armv7Binary}
			if arch == "arm64" {
				expected["arm64"] = options.Arm64Binary
			}
			bundleHelpers(t, output, expected)
		})
	}
}

func TestBuildReplacesPayloadInPlaceWithoutGrowing(t *testing.T) {
	options := Options{Arch: "arm64", Binary: elfFixture(t, "arm64"), Armv7Binary: elfFixture(t, "armv7"), Arm64Binary: elfFixture(t, "arm64")}
	if err := Build(t.Context(), options); err != nil {
		t.Fatal(err)
	}
	first, _ := os.Stat(options.Binary)
	if err := Build(t.Context(), options); err != nil {
		t.Fatal(err)
	}
	second, _ := os.Stat(options.Binary)
	if first.Size() != second.Size() {
		t.Fatalf("rebundling grew executable: %d -> %d", first.Size(), second.Size())
	}
	bundleHelpers(t, options.Binary, map[string]string{"armv7": options.Armv7Binary, "arm64": options.Arm64Binary})
}

func TestBuildRejectsWrongHelperBeforePublishing(t *testing.T) {
	for _, inPlace := range []bool{false, true} {
		input := elfFixture(t, "arm64")
		original, _ := os.ReadFile(input)
		output := ""
		if !inPlace {
			output = filepath.Join(t.TempDir(), "receiver")
		}
		err := Build(t.Context(), Options{Arch: "arm64", Binary: input, Output: output, Armv7Binary: elfFixture(t, "arm64"), Arm64Binary: elfFixture(t, "arm64")})
		if err == nil {
			t.Fatal("wrong helper architecture accepted")
		}
		if output != "" {
			if _, err := os.Stat(output); !os.IsNotExist(err) {
				t.Fatalf("published incomplete output: %v", err)
			}
		}
		inputAfter, _ := os.ReadFile(input)
		if !bytes.Equal(inputAfter, original) {
			t.Fatal("failed build modified input")
		}
	}
}

func TestBuildRejectsUnknownPackageArchitecture(t *testing.T) {
	if err := Build(t.Context(), Options{Arch: "amd64", Binary: elfFixture(t, "arm64")}); err == nil {
		t.Fatal("unsupported package architecture accepted")
	}
}

func TestUPXCommandHelper(t *testing.T) {
	if os.Getenv("STATION_PCM_UPX_TEST") != "1" {
		return
	}
	separator := slices.Index(os.Args, "--")
	if separator < 0 || separator+1 == len(os.Args) {
		os.Exit(2)
	}
	args := os.Args[separator+1:]
	stage := "compression"
	if args[0] == "-t" {
		stage = "integrity"
	}
	if os.Getenv("STATION_PCM_UPX_FAIL") == stage {
		os.Exit(7)
	}
	data, err := os.ReadFile(args[len(args)-1])
	if err != nil {
		os.Exit(3)
	}
	if _, err := zip.NewReader(bytes.NewReader(data), int64(len(data))); err == nil {
		// Compressing an executable with its old owned overlay was the defect.
		os.Exit(4)
	}
	switch stage {
	case "compression":
		if len(args) != 3 || args[0] != "--ultra-brute" || args[1] != "--no-lzma" {
			os.Exit(5)
		}
		want, err := os.ReadFile(os.Getenv("STATION_PCM_UPX_HOST"))
		if err != nil || !bytes.Equal(data, want) {
			os.Exit(6)
		}
		if err := os.WriteFile(args[2], append(data, []byte("packed")...), 0o750); err != nil {
			os.Exit(8)
		}
	case "integrity":
		if len(args) != 2 || !bytes.HasSuffix(data, []byte("packed")) {
			os.Exit(9)
		}
	}
	log, err := os.OpenFile(os.Getenv("STATION_PCM_UPX_LOG"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		os.Exit(10)
	}
	if _, err := log.WriteString(stage + "\n"); err != nil {
		os.Exit(11)
	}
	if err := log.Close(); err != nil {
		os.Exit(12)
	}
	os.Exit(0)
}

func fakeUPX(t *testing.T, host string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("UPX command fixture requires a Unix shell")
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	quoted := "'" + strings.ReplaceAll(executable, "'", "'\\''") + "'"
	script := "#!/bin/sh\nexec " + quoted + " -test.run=^TestUPXCommandHelper$ -- \"$@\"\n"
	if err := os.WriteFile(filepath.Join(directory, "upx"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", directory+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("STATION_PCM_UPX_TEST", "1")
	t.Setenv("STATION_PCM_UPX_HOST", host)
	log := filepath.Join(t.TempDir(), "upx.log")
	t.Setenv("STATION_PCM_UPX_LOG", log)
	return log
}

func TestBuildStripsOwnedPayloadBeforeCompression(t *testing.T) {
	input := elfFixture(t, "arm64")
	original, _ := os.ReadFile(input)
	host := elfFixture(t, "arm64")
	log := fakeUPX(t, host)
	options := Options{Arch: "arm64", Binary: input, Armv7Binary: elfFixture(t, "armv7"), Arm64Binary: elfFixture(t, "arm64")}
	if err := Build(t.Context(), options); err != nil {
		t.Fatal(err)
	}
	bundledInput, _ := os.ReadFile(input)
	options.Output = filepath.Join(t.TempDir(), "receiver")
	options.UPX = true
	if err := Build(t.Context(), options); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(options.Output)
	if !bytes.HasPrefix(got, append(bytes.Clone(original), []byte("packed")...)) {
		t.Fatal("compressed host prefix was not preserved")
	}
	inputAfter, _ := os.ReadFile(input)
	if !bytes.Equal(inputAfter, bundledInput) {
		t.Fatal("compression modified the supplied input")
	}
	calls, err := os.ReadFile(log)
	if err != nil || string(calls) != "compression\nintegrity\n" {
		t.Fatalf("wrong UPX sequence: %q %v", calls, err)
	}
	bundleHelpers(t, options.Output, map[string]string{"armv7": options.Armv7Binary, "arm64": options.Arm64Binary})
}

func TestBuildCompressionFailureDoesNotPublish(t *testing.T) {
	for _, stage := range []string{"compression", "integrity"} {
		t.Run(stage, func(t *testing.T) {
			input := elfFixture(t, "arm64")
			original, _ := os.ReadFile(input)
			fakeUPX(t, input)
			t.Setenv("STATION_PCM_UPX_FAIL", stage)
			options := Options{Arch: "arm64", Binary: input, Armv7Binary: elfFixture(t, "armv7"), Arm64Binary: elfFixture(t, "arm64"), UPX: true}
			if err := Build(t.Context(), options); err == nil {
				t.Fatal("UPX failure was ignored")
			}
			got, _ := os.ReadFile(input)
			if !bytes.Equal(got, original) {
				t.Fatal("failed compression changed the supplied executable")
			}
		})
	}
}
