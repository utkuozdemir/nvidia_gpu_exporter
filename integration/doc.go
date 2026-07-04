// Package integration holds the end-to-end tests: the real exporter entry
// running in-process against the fake nvidia-smi replaying the captures under
// testdata/captures, with the scraped metrics compared to golden files.
//
// The golden matrix is discovered from the capture files themselves; a new
// capture is picked up automatically and fails the suite until its golden
// file is generated (and reviewed) with:
//
//	go test ./integration/ -update
package integration
