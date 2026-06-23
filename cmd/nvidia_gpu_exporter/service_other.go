//go:build !windows

package main

// dispatch runs the exporter. On non-Windows platforms there is no service
// manager to integrate with, so it always runs interactively.
func dispatch() error {
	return runInteractive()
}
