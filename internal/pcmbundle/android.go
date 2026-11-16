package pcmbundle

import (
	"bytes"
	"debug/elf"
	"errors"
	"fmt"
	"slices"
)

// The same verified helper is used on both systems. Android's linker resolves
// dynamic imports before main, so adapt only its ELF loader and library names
// in the private runtime copy. No executable code or relocation is modified.
func prepareNativeHelper(data []byte, arch string, environment []string) ([]byte, error) {
	if !slices.Contains(environment, "STATION_PCM_BACKEND=opensl") {
		return data, nil
	}
	if arch != "arm64" {
		return nil, errors.New("Android PCM currently requires an ARM64 helper")
	}
	return prepareAndroidHelper(data)
}

func prepareAndroidHelper(original []byte) ([]byte, error) {
	file, err := elf.NewFile(bytes.NewReader(original))
	if err != nil {
		return nil, errors.New("invalid Android PCM ELF payload")
	}
	defer file.Close()
	if file.Class != elf.ELFCLASS64 || file.Data != elf.ELFDATA2LSB || file.Machine != elf.EM_AARCH64 || file.Type != elf.ET_DYN {
		return nil, errors.New("Android PCM needs an ARM64 PIE helper; rebuild with station-pcm-package")
	}
	data := slices.Clone(original)
	loaderFound := false
	for _, program := range file.Progs {
		if program.Type != elf.PT_INTERP {
			continue
		}
		if loaderFound {
			return nil, errors.New("PCM helper has multiple ELF interpreters")
		}
		loaderFound = true
		if program.Off > uint64(len(data)) || program.Filesz > uint64(len(data))-program.Off {
			return nil, errors.New("invalid PCM interpreter range")
		}
		region := data[program.Off : program.Off+program.Filesz]
		old := bytes.TrimRight(region, "\x00")
		if string(old) != "/lib/ld-linux-aarch64.so.1" && string(old) != "/system/bin/linker64" {
			return nil, errors.New("unsupported PCM helper ELF interpreter")
		}
		if err := replaceELFString(region, "/system/bin/linker64"); err != nil {
			return nil, err
		}
	}
	if !loaderFound {
		return nil, errors.New("PCM helper ELF interpreter is missing")
	}
	dynamic := file.SectionByType(elf.SHT_DYNAMIC)
	if dynamic == nil || dynamic.Link >= uint32(len(file.Sections)) || dynamic.Size%16 != 0 {
		return nil, errors.New("PCM helper dynamic table is missing or invalid")
	}
	stringsSection := file.Sections[dynamic.Link]
	if stringsSection.Type != elf.SHT_STRTAB || stringsSection.Offset > uint64(len(data)) || stringsSection.Size > uint64(len(data))-stringsSection.Offset || dynamic.Offset > uint64(len(data)) || dynamic.Size > uint64(len(data))-dynamic.Offset {
		return nil, errors.New("invalid PCM helper dynamic string range")
	}
	stringsData := data[stringsSection.Offset : stringsSection.Offset+stringsSection.Size]
	needed := 0
	var references []uint64
	var rewritten [][2]uint64
	for offset := dynamic.Offset; offset < dynamic.Offset+dynamic.Size; offset += 16 {
		tag := elf.DynTag(file.ByteOrder.Uint64(data[offset:]))
		value := file.ByteOrder.Uint64(data[offset+8:])
		if tag == elf.DT_NULL {
			break
		}
		if tag == elf.DT_VERNEED || tag == elf.DT_VERNEEDNUM {
			return nil, errors.New("versioned glibc imports cannot be used by Android PCM")
		}
		if tag != elf.DT_NEEDED {
			switch tag {
			case elf.DT_SONAME, elf.DT_RPATH, elf.DT_RUNPATH, elf.DT_AUXILIARY, elf.DT_FILTER:
				if _, err := elfStringAt(stringsData, value); err != nil {
					return nil, err
				}
				references = append(references, value)
			}
			continue
		}
		references = append(references, value)
		region, err := elfStringAt(stringsData, value)
		if err != nil {
			return nil, err
		}
		old := string(region[:len(region)-1])
		replacement := ""
		switch old {
		case "libdl.so.2", "libdl.so":
			replacement = "libdl.so"
		case "libc.so.6", "libpthread.so.0", "librt.so.1", "libc.so":
			replacement = "libc.so"
		case "libm.so.6", "libm.so":
			replacement = "libm.so"
		default:
			return nil, fmt.Errorf("unsupported PCM helper dynamic library %q", old)
		}
		rewritten = append(rewritten, [2]uint64{value, value + uint64(len(region))})
		if err := replaceELFString(region, replacement); err != nil {
			return nil, err
		}
		needed++
	}
	if needed == 0 {
		return nil, errors.New("PCM helper has no native dynamic imports")
	}
	// purego may retain errno's glibc spelling when uid-related code is linked.
	// Bionic exports the same function contract as __errno instead.
	for _, symbols := range file.Sections {
		if symbols.Type != elf.SHT_DYNSYM {
			continue
		}
		if symbols.Link != dynamic.Link || symbols.Entsize != 24 || symbols.Size%24 != 0 || symbols.Offset > uint64(len(data)) || symbols.Size > uint64(len(data))-symbols.Offset {
			return nil, errors.New("invalid PCM helper dynamic symbols")
		}
		for offset := symbols.Offset; offset < symbols.Offset+symbols.Size; offset += 24 {
			nameOffset := uint64(file.ByteOrder.Uint32(data[offset:]))
			references = append(references, nameOffset)
			region, err := elfStringAt(stringsData, nameOffset)
			if err != nil {
				return nil, err
			}
			if file.ByteOrder.Uint16(data[offset+6:]) != uint16(elf.SHN_UNDEF) {
				continue
			}
			if string(region[:len(region)-1]) == "__errno_location" {
				rewritten = append(rewritten, [2]uint64{nameOffset, nameOffset + uint64(len(region))})
				if err := replaceELFString(region, "__errno"); err != nil {
					return nil, err
				}
			}
		}
	}
	// Do not alter an aliased suffix used by another library or symbol name.
	for _, region := range rewritten {
		for _, reference := range references {
			if reference > region[0] && reference < region[1] {
				return nil, errors.New("PCM helper dynamic strings share a rewritten suffix")
			}
		}
	}
	prepared, err := elf.NewFile(bytes.NewReader(data))
	if err != nil {
		return nil, errors.New("prepared Android PCM ELF is invalid")
	}
	defer prepared.Close()
	libraries, err := prepared.DynString(elf.DT_NEEDED)
	if err != nil || len(libraries) == 0 {
		return nil, errors.New("prepared Android PCM dynamic imports are invalid")
	}
	for _, library := range libraries {
		if library != "libc.so" && library != "libdl.so" && library != "libm.so" {
			return nil, errors.New("prepared Android PCM has a non-Bionic import")
		}
	}
	return data, nil
}

func elfStringAt(table []byte, offset uint64) ([]byte, error) {
	if offset >= uint64(len(table)) {
		return nil, errors.New("PCM helper dynamic string offset is out of range")
	}
	end := bytes.IndexByte(table[offset:], 0)
	if end < 0 {
		return nil, errors.New("PCM helper dynamic string is unterminated")
	}
	return table[offset : offset+uint64(end)+1], nil
}
func replaceELFString(region []byte, value string) error {
	if len(value)+1 > len(region) {
		return errors.New("PCM helper ELF string has insufficient space")
	}
	clear(region)
	copy(region, value)
	return nil
}
