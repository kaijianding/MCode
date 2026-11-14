//go:build unix

package main

import (
	"syscall"
	"testing"
	"time"
)

// Without a handler SIGUSR1 terminates the process, so this also proves the
// signal no longer kills a running build.
func TestStartPprofFromSignal(t *testing.T) {
	orig := pprofStart
	t.Cleanup(func() { pprofStart = orig })
	started := make(chan string, 1)
	pprofStart = func(addr string) { started <- addr }

	startPprofFromSignal()
	if err := syscall.Kill(syscall.Getpid(), syscall.SIGUSR1); err != nil {
		t.Fatal(err)
	}

	select {
	case addr := <-started:
		if addr != pprofSignalAddr {
			t.Errorf("pprof started on %q, want %q", addr, pprofSignalAddr)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("SIGUSR1 started no profiler")
	}
}
