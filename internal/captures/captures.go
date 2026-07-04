// Package captures holds the recorded nvidia-smi outputs from real machines
// and embeds them, so the fake nvidia-smi and the tests can replay any of
// them without a repository checkout. The file format is what
// internal/capture parses; README.md in this directory documents the corpus
// and how to contribute a capture.
//
// This package is test/development data. It must never be imported by the
// shipped exporter binary; a test guards that.
package captures

import "embed"

// FS holds every committed capture. go:embed picks new files up
// automatically, so contributing a capture extends the corpus with no code
// change.
//
//go:embed *.txt
var FS embed.FS

// Default is the capture replayed when the caller picks none: a common
// consumer setup on the newest driver we have a capture for.
const Default = "linux-x86_64__nvidia-geforce-rtx-2080-super__595.71.05.txt"
