//go:build !unix

package main

// startPprofFromSignal does nothing: SIGUSR1 does not exist on Windows.
func startPprofFromSignal() {}
