package pcmbundle

import (
	"bytes"
	"crypto/sha256"
	"debug/elf"
	"encoding/binary"
	"encoding/hex"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func androidELFFixture() []byte {
	data := make([]byte, 0x600)
	copy(data, "\x7fELF\x02\x01\x01")
	binary.LittleEndian.PutUint16(data[16:], uint16(elf.ET_DYN))
	binary.LittleEndian.PutUint16(data[18:], uint16(elf.EM_AARCH64))
	binary.LittleEndian.PutUint32(data[20:], 1)
	binary.LittleEndian.PutUint64(data[32:], 64)
	binary.LittleEndian.PutUint64(data[40:], 0x100)
	binary.LittleEndian.PutUint16(data[52:], 64)
	binary.LittleEndian.PutUint16(data[54:], 56)
	binary.LittleEndian.PutUint16(data[56:], 1)
	binary.LittleEndian.PutUint16(data[58:], 64)
	binary.LittleEndian.PutUint16(data[60:], 6)
	binary.LittleEndian.PutUint16(data[62:], 1)
	binary.LittleEndian.PutUint32(data[64:], uint32(elf.PT_INTERP))
	binary.LittleEndian.PutUint64(data[72:], 0x80)
	binary.LittleEndian.PutUint64(data[96:], 32)
	copy(data[0x80:], "/lib/ld-linux-aarch64.so.1\x00")
	sectionNames := []byte("\x00.shstrtab\x00.dynstr\x00.dynamic\x00.dynsym\x00.text\x00")
	copy(data[0x300:], sectionNames)
	names := []byte("\x00libdl.so.2\x00libc.so.6\x00libpthread.so.0\x00__errno_location\x00")
	copy(data[0x380:], names)
	section := func(index, name int, kind elf.SectionType, offset, size, link, entry uint64) {
		p := data[0x100+index*64:]
		binary.LittleEndian.PutUint32(p, uint32(name))
		binary.LittleEndian.PutUint32(p[4:], uint32(kind))
		binary.LittleEndian.PutUint64(p[24:], offset)
		binary.LittleEndian.PutUint64(p[32:], size)
		binary.LittleEndian.PutUint32(p[40:], uint32(link))
		binary.LittleEndian.PutUint64(p[56:], entry)
	}
	section(1, 1, elf.SHT_STRTAB, 0x300, uint64(len(sectionNames)), 0, 0)
	section(2, 11, elf.SHT_STRTAB, 0x380, uint64(len(names)), 0, 0)
	section(3, 19, elf.SHT_DYNAMIC, 0x480, 64, 2, 16)
	section(4, 28, elf.SHT_DYNSYM, 0x500, 48, 2, 24)
	section(5, 36, elf.SHT_PROGBITS, 0x580, 80, 0, 0)
	for index, name := range []string{"libdl.so.2", "libc.so.6", "libpthread.so.0"} {
		p := data[0x480+index*16:]
		binary.LittleEndian.PutUint64(p, uint64(elf.DT_NEEDED))
		binary.LittleEndian.PutUint64(p[8:], uint64(bytes.Index(names, []byte(name+"\x00"))))
	}
	binary.LittleEndian.PutUint32(data[0x500+24:], uint32(bytes.Index(names, []byte("__errno_location\x00"))))
	copy(data[0x580:], "executable-data libc.so.6\x00libdl.so.2\x00__errno_location\x00")
	return data
}

func TestAndroidPreparationChangesOnlyLoaderAndReferencedDynamicStrings(t *testing.T) {
	original := androidELFFixture()
	snapshot := slices.Clone(original)
	prepared, err := prepareAndroidHelper(original)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(original, snapshot) {
		t.Fatal("embedded original payload modified")
	}
	for index, value := range original {
		if index >= 0x80 && index < 0xa0 || index >= 0x380 && index < 0x380+57 {
			continue
		}
		if prepared[index] != value {
			t.Fatalf("non-string ELF byte changed at %x", index)
		}
	}
	file, err := elf.NewFile(bytes.NewReader(prepared))
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	needed, err := file.DynString(elf.DT_NEEDED)
	if err != nil || !slices.Equal(needed, []string{"libdl.so", "libc.so", "libc.so"}) {
		t.Fatal(needed, err)
	}
	symbols, err := file.DynamicSymbols()
	if err != nil || len(symbols) != 1 || symbols[0].Name != "__errno" {
		t.Fatal("errno mapping", symbols, err)
	}
	loader := bytes.TrimRight(prepared[0x80:0xa0], "\x00")
	if string(loader) != "/system/bin/linker64" {
		t.Fatal("wrong loader", string(loader))
	}
	if !bytes.Equal(prepared[0x580:], original[0x580:]) {
		t.Fatal("code or unrelated strings changed")
	}
	repeated, err := prepareAndroidHelper(prepared)
	if err != nil || !bytes.Equal(repeated, prepared) {
		t.Fatal("preparation not idempotent", err)
	}
	linux, err := prepareNativeHelper(original, "arm64", nil)
	if err != nil || !bytes.Equal(linux, original) {
		t.Fatal("Linux payload changed", err)
	}
}

func TestAndroidPreparationRejectsUnsupportedAndCorruptELF(t *testing.T) {
	for _, name := range []string{"not-elf", "exe", "wrong-arch", "bad-loader", "interpreter-range", "dynamic-range", "string-offset", "unknown-library", "versioned-import", "aliased-symbol", "armv7"} {
		t.Run(name, func(t *testing.T) {
			data := androidELFFixture()
			arch := "arm64"
			switch name {
			case "not-elf":
				data = []byte("bad")
			case "exe":
				binary.LittleEndian.PutUint16(data[16:], uint16(elf.ET_EXEC))
			case "wrong-arch":
				binary.LittleEndian.PutUint16(data[18:], uint16(elf.EM_X86_64))
			case "bad-loader":
				copy(data[0x80:0xa0], "/another/loader\x00")
			case "interpreter-range":
				binary.LittleEndian.PutUint64(data[72:], 0xffff)
			case "dynamic-range":
				binary.LittleEndian.PutUint64(data[0x100+3*64+24:], 0xffff)
			case "string-offset":
				binary.LittleEndian.PutUint64(data[0x480+8:], 0xffff)
			case "unknown-library":
				copy(data[0x381:], "badlib.so\x00")
			case "versioned-import":
				binary.LittleEndian.PutUint64(data[0x480:], uint64(elf.DT_VERNEED))
			case "aliased-symbol":
				binary.LittleEndian.PutUint32(data[0x500+24:], 2)
			case "armv7":
				arch = "armv7"
			}
			if _, err := prepareNativeHelper(data, arch, []string{"STATION_PCM_BACKEND=opensl"}); err == nil {
				t.Fatal("invalid Android helper accepted")
			}
		})
	}
}

func TestResolveUsesPreparedOutputDigestAndPreservesEmbeddedPayload(t *testing.T) {
	original := androidELFFixture()
	prepared, err := prepareAndroidHelper(original)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "receiver")
	if err := os.WriteFile(path, elfFixture("arm64"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := Append(t.Context(), path, map[string][]byte{"arm64": original}); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(path)
	state := t.TempDir()
	selected := func() (string, []string, error) { return "arm64", []string{"STATION_PCM_BACKEND=opensl"}, nil }
	command, _, err := resolve(t.Context(), path, state, selected)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(prepared)
	expected := filepath.Join(state, "pcm", hex.EncodeToString(digest[:]), "station-pcm")
	if command != expected {
		t.Fatal("cache key did not use transformed bytes", command, expected)
	}
	cached, _ := os.ReadFile(command)
	if !bytes.Equal(cached, prepared) {
		t.Fatal("cached helper differs from prepared payload")
	}
	after, _ := os.ReadFile(path)
	if !bytes.Equal(after, before) {
		t.Fatal("receiver payload changed")
	}
	os.WriteFile(command, []byte("stale cache"), 0700)
	if _, _, err := resolve(t.Context(), path, state, selected); err != nil {
		t.Fatal(err)
	}
	cached, _ = os.ReadFile(command)
	if !bytes.Equal(cached, prepared) {
		t.Fatal("prepared cache was not repaired")
	}
}

func TestAndroidNativeEnvironmentChecksBothLibraryAndLoader(t *testing.T) {
	root := t.TempDir()
	library := filepath.Join(root, "system/lib64/libOpenSLES.so")
	loader := filepath.Join(root, "system/bin/linker64")
	for _, path := range []string{library, loader} {
		os.MkdirAll(filepath.Dir(path), 0700)
		os.WriteFile(path, elfFixture("arm64"), 0600)
	}
	t.Setenv("LD_LIBRARY_PATH", "/existing")
	arch, environment, err := androidNativeEnvironment(root, "arm64", "")
	if err != nil || arch != "arm64" || !slices.Contains(environment, "STATION_PCM_BACKEND=opensl") || !slices.Contains(environment, "STATION_PCM_LIBRARY="+library) || !slices.Contains(environment, "LD_LIBRARY_PATH="+filepath.Dir(library)+":/existing") {
		t.Fatal(arch, environment, err)
	}
	if _, _, err := androidNativeEnvironment(root, "arm", ""); err == nil {
		t.Fatal("Android ARMv7 accepted")
	}
	os.WriteFile(loader, elfFixture("armv7"), 0600)
	if _, _, err := androidNativeEnvironment(root, "arm64", ""); err == nil {
		t.Fatal("wrong loader ABI accepted")
	}
}
