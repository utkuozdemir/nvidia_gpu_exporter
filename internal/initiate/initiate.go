// Package initiate This package allows us to initiate Time
// Sensitive components (Like registering the windows service)
// as early as possible in the startup process
package initiate

// StopCh is used by Windows service.
//
//nolint:gochecknoglobals
var StopCh = make(chan bool)
