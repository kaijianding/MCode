package main

import (
	"log"
	"net"
	"net/http"
	"net/http/pprof"
	"os"
	"runtime"
	"sync/atomic"
	"time"
)

// pprofStart starts the profiler on an address pprofAddr has already
// validated. It is a variable so tests can exercise the signal path without
// binding a port.
var pprofStart = startPprof

func pprofAddr(raw string) (string, bool) {
	host, port, err := net.SplitHostPort(raw)
	if err != nil || port == "" || (host != "127.0.0.1" && host != "::1" && host != "localhost") {
		return "", false
	}
	return raw, true
}

func startPprofFromEnv() {
	raw := os.Getenv("MCODE_PPROF")
	if raw == "" {
		return
	}
	addr, ok := pprofAddr(raw)
	if !ok {
		log.Printf("invalid MCODE_PPROF address: %q", raw)
		return
	}
	pprofStart(addr)
}

// pprofUp turns the profiler on once. MCODE_PPROF at startup and SIGUSR1 while
// running both come through here, so whichever fires first owns the server.
var pprofUp atomic.Bool

// startPprof serves the runtime profiles on addr until the process exits.
func startPprof(addr string) {
	if !pprofUp.CompareAndSwap(false, true) {
		log.Printf("pprof server already running")
		return
	}
	runtime.SetMutexProfileFraction(5)
	runtime.SetBlockProfileRate(1000000)

	mux := http.NewServeMux()
	mux.HandleFunc("/debug/pprof/", pprof.Index)
	mux.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
	mux.HandleFunc("/debug/pprof/profile", pprof.Profile)
	mux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
	mux.HandleFunc("/debug/pprof/trace", pprof.Trace)

	// Listen before claiming the port is up: a failure here should leave the
	// next trigger able to retry instead of silently disabling profiling.
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		pprofUp.Store(false)
		log.Printf("pprof server on %s: %v", addr, err)
		return
	}
	server := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	log.Printf("starting pprof server on %s", addr)
	go func() {
		if err := server.Serve(ln); err != nil {
			log.Printf("pprof server: %v", err)
		}
	}()
}
