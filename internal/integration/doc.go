// Package integration holds the end-to-end tests: the real exporter entry
// running in-process against the fake nvidia-smi replaying the embedded
// corpus from internal/captures, with the scraped metrics compared to the
// expected output files under testdata.
//
// The replay matrix is discovered from the capture files themselves; a new
// capture is picked up automatically and fails the suite until its expected
// output is generated (and reviewed) with:
//
//	go test ./internal/integration/ -update
package integration
