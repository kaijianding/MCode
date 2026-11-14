//go:build unix

package main

import (
	"log"
	"os"
	"os/signal"
	"syscall"
)

// pprofSignalAddr is where SIGUSR1 serves profiles: a build already running
// must be profileable without a restart.
const pprofSignalAddr = "127.0.0.1:6060"

// startPprofFromSignal turns SIGUSR1 into "serve profiles on pprofSignalAddr".
func startPprofFromSignal() {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGUSR1)
	go func() {
		for range ch {
			log.Printf("SIGUSR1: pprof on %s", pprofSignalAddr)
			pprofStart(pprofSignalAddr)
		}
	}()
}
