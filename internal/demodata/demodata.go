// Package demodata embeds the small curated capture set the exporter's demo
// backend serves synthetic data from. It is deliberately separate from the
// full test corpus in internal/captures: the corpus must never be embedded
// into the exporter binary (a guard test enforces it), while these two
// captures are a deliberate, size-budgeted part of the demo feature.
package demodata

import "embed"

// FS holds the curated captures: a consumer RTX 4080 SUPER box and an H200
// datacenter card, both replicable into multi-GPU shapes by the fake's gpus
// setting.
//
//go:embed *.txt
var FS embed.FS

// Default is the capture the demo backend serves when none is configured.
const Default = "linux-x86_64__nvidia-h200__590.48.01.txt"
