package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"testing"
	"time"
)

func TestInheritedStdinReadStopsOnSIGTERM(t *testing.T) {
	if os.Getenv("STATION_PCM_STDIN_CHILD") == "1" {
		ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM)
		defer stop()
		input, err := interruptibleInput(os.Stdin)
		if err != nil {
			os.Exit(4)
		}
		defer input.Close()
		cancelRead := context.AfterFunc(ctx, func() { input.Close() })
		defer cancelRead()
		fmt.Fprintln(os.Stdout, "ready")
		_, err = io.ReadFull(input, make([]byte, 4))
		if err == nil {
			os.Exit(2)
		}
		if ctx.Err() == nil {
			os.Exit(3)
		}
		return
	}
	read, write, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer read.Close()
	defer write.Close()
	command := exec.Command(os.Args[0], "-test.run=^TestInheritedStdinReadStopsOnSIGTERM$")
	command.Env = append(os.Environ(), "STATION_PCM_STDIN_CHILD=1")
	command.Stdin = read
	stdout, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err = command.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- command.Wait(); close(done) }()
	defer func() { command.Process.Kill(); <-done }()
	scanner := bufio.NewScanner(stdout)
	if !scanner.Scan() || scanner.Text() != "ready" {
		t.Fatal("child not ready")
	}
	// Keep the writer open so EOF cannot make this test pass accidentally.
	if err = command.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	select {
	case err = <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("SIGTERM did not interrupt inherited stdin Read")
	}
}
