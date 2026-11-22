// Package pcmpackage builds and embeds owned PCM payloads into one executable.
package pcmpackage

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/skrashevich/station-airplay/internal/pcmbundle"
)

type Options struct {
	Arch        string
	Binary      string
	Output      string
	Armv7Binary string
	Arm64Binary string
	UPX         bool
}

// Build copies the input executable, builds or reads PCM helpers, and appends
// their bundle before atomically publishing one output file. Empty Output means
// replace Binary in place. Tests, installation and firmware changes are callers' work.
func Build(ctx context.Context, options Options) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if options.Arch != "armv7" && options.Arch != "arm64" {
		return fmt.Errorf("unsupported package architecture %q", options.Arch)
	}
	if options.Binary == "" {
		return fmt.Errorf("input executable is required")
	}
	input, err := openELF(options.Binary, options.Arch)
	if err != nil {
		return err
	}
	defer input.Close()
	info, err := input.Stat()
	if err != nil {
		return err
	}
	output := options.Output
	if output == "" {
		output = options.Binary
	}
	if err := os.MkdirAll(filepath.Dir(output), 0o755); err != nil {
		return err
	}
	stage, err := os.MkdirTemp(filepath.Dir(output), ".station-pcm-package-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	copied, err := os.CreateTemp(stage, "receiver-*")
	if err != nil {
		return err
	}
	executable := copied.Name()
	_, copyErr := io.Copy(copied, input)
	inputCloseErr := input.Close()
	closeErr := copied.Close()
	if copyErr != nil {
		return copyErr
	}
	if closeErr != nil {
		return closeErr
	}
	if inputCloseErr != nil {
		return inputCloseErr
	}
	if err := os.Chmod(executable, info.Mode().Perm()); err != nil {
		return err
	}
	targets := []string{"armv7"}
	if options.Arch == "arm64" {
		targets = append(targets, "arm64")
	}
	helpers := make(map[string][]byte, len(targets))
	for _, arch := range targets {
		path := options.Armv7Binary
		goarch, loader := "arm", "/lib/ld-linux-armhf.so.3"
		if arch == "arm64" {
			path, goarch, loader = options.Arm64Binary, "arm64", "/lib/ld-linux-aarch64.so.1"
		}
		if path == "" {
			path = filepath.Join(stage, "station-pcm-"+arch)
			// Full import path also resolves from an embedding application's vendor tree.
			arguments := []string{"build", "-trimpath", "-ldflags=-s -w -I " + loader}
			// ARM64 PIE runs under glibc unchanged and can be prepared for Bionic at
			// runtime. Keep ARMv7's established linker mode for older Linux firmware.
			if arch == "arm64" {
				arguments = append(arguments, "-buildmode=pie")
			}
			arguments = append(arguments, "-o", path, "github.com/skrashevich/station-airplay/cmd/station-pcm")
			command := exec.CommandContext(ctx, "go", arguments...)
			command.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS=linux", "GOARCH="+goarch, "GOARM=7")
			command.Stdout, command.Stderr = os.Stderr, os.Stderr
			if err := command.Run(); err != nil {
				return fmt.Errorf("build %s helper: %w", arch, err)
			}
		}
		helper, err := openELF(path, arch)
		if err != nil {
			return err
		}
		data, err := io.ReadAll(helper)
		helper.Close()
		if err != nil {
			return err
		}
		helpers[arch] = data
	}
	if options.UPX {
		// UPX must only see the host ELF. Compressing an existing ZIP overlay
		// adds UPX metadata after it, which cannot safely be discarded on rebundle.
		if _, err := pcmbundle.Strip(ctx, executable); err != nil {
			return fmt.Errorf("remove PCM payload before compression: %w", err)
		}
		for _, args := range [][]string{{"--ultra-brute", "--no-lzma", executable}, {"-t", executable}} {
			command := exec.CommandContext(ctx, "upx", args...)
			command.Stdout, command.Stderr = os.Stderr, os.Stderr
			if err := command.Run(); err != nil {
				return fmt.Errorf("UPX %s: %w (use an unpacked input with -upx)", args[0], err)
			}
		}
	}
	if err := pcmbundle.Append(ctx, executable, helpers); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return os.Rename(executable, output)
}

// Inputs are CLI-selected files or helpers built in the private staging directory.
// Return the validated descriptor so packaging never reopens the path.
func openELF(path, arch string) (*os.File, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	header := make([]byte, 20)
	_, err = file.ReadAt(header, 0)
	if err != nil || string(header[:4]) != "\x7fELF" || header[5] != 1 {
		file.Close()
		return nil, fmt.Errorf("%s is not a little-endian Linux ELF", filepath.Base(path))
	}
	class, machine := byte(1), uint16(40)
	if arch == "arm64" {
		class, machine = 2, 183
	}
	if header[4] != class || binary.LittleEndian.Uint16(header[18:]) != machine {
		file.Close()
		return nil, fmt.Errorf("%s has a different ELF architecture than %s", filepath.Base(path), arch)
	}
	return file, nil
}
