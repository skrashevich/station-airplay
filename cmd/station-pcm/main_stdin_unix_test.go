//go:build unix

package main

import (
	"os"
	"os/exec"
	"testing"

	"golang.org/x/sys/unix"
)

func TestNonPlaybackCommandsLeaveInheritedStdinFlagsAlone(t *testing.T) {
	if os.Getenv("STATION_PCM_NO_INPUT_CHILD") == "1" {
		os.Args = []string{"station-pcm", os.Getenv("STATION_PCM_ARG")}
		main()
		return
	}
	for _, arg := range []string{"--version", "--check", "--unknown"} {
		t.Run(arg, func(t *testing.T) {
			read, write, err := os.Pipe()
			if err != nil {
				t.Fatal(err)
			}
			defer read.Close()
			defer write.Close()
			// Keep the raw fd: calling File.Fd again could itself restore blocking mode.
			fd := read.Fd()
			before, err := unix.FcntlInt(fd, unix.F_GETFL, 0)
			if err != nil {
				t.Fatal(err)
			}
			command := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestNonPlaybackCommandsLeaveInheritedStdinFlagsAlone$")
			command.Env = append(os.Environ(), "STATION_PCM_NO_INPUT_CHILD=1", "STATION_PCM_ARG="+arg)
			command.Stdin = read
			// Unsupported-platform --check and invalid arguments may exit with an error.
			// Neither is allowed to alter the shell's inherited input description.
			command.CombinedOutput()
			after, err := unix.FcntlInt(fd, unix.F_GETFL, 0)
			if err != nil {
				t.Fatal(err)
			}
			if after != before {
				t.Fatalf("stdin flags changed: %#x -> %#x", before, after)
			}
		})
	}
}
