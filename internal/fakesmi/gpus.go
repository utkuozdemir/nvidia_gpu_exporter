package fakesmi

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/utkuozdemir/nvidia_gpu_exporter/internal/nvidiasmi"
)

// maxGPUs bounds the simulated GPU count: the identities derive a PCI bus id
// per GPU, and the bus byte has 255 usable values.
const maxGPUs = 255

// reservedIdentityFields are the query fields that carry a GPU's identity.
// While gpus is active they come exclusively from the gpus entries (or the
// generator): a shared override would stamp the same uuid on every simulated
// GPU, and the exporter, which labels all per-GPU metrics by uuid alone,
// would then emit duplicate label sets and fail the whole scrape.
var reservedIdentityFields = map[string]struct{}{
	"uuid": {}, "serial": {}, "index": {}, "count": {},
	"pci.bus_id": {}, "pci.bus": {},
	"gpu_uuid": {}, "gpu_serial": {}, "gpu_bus_id": {},
}

// gpuIdentity is one simulated GPU's resolved identity. An empty serial keeps
// the captured cell.
type gpuIdentity struct {
	uuid   string
	serial string
}

// gpusFileConfig is the yaml form of the gpus setting: either a scalar count
// (identical cards with generated identities, same as --gpus) or a sequence
// of per-GPU entries.
type gpusFileConfig struct {
	count   int
	entries []gpuFileEntry
}

// gpuFileEntry is one per-GPU config entry: an optional explicit identity and
// optional overrides that layer on top of the top-level ones for this GPU
// only.
type gpuFileEntry struct {
	uuid      string
	serial    string
	overrides map[string]overrideEntry
}

// UnmarshalYAML accepts a scalar count or a sequence of per-GPU entries, and
// rejects anything else.
func (g *gpusFileConfig) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind == yaml.ScalarNode {
		count, err := strconv.Atoi(node.Value)
		if err != nil {
			return fmt.Errorf("gpus must be a count or a list of GPU entries, got %q", node.Value)
		}

		g.count = count

		return validateGPUCount(count)
	}

	if node.Kind != yaml.SequenceNode {
		return errors.New("gpus must be a count or a list of GPU entries")
	}

	for _, item := range node.Content {
		entry, err := decodeGPUEntry(item)
		if err != nil {
			return err
		}

		g.entries = append(g.entries, entry)
	}

	return validateGPUCount(len(g.entries))
}

// decodeGPUEntry strictly decodes one per-GPU entry: an unknown key is an
// error, matching the top-level config's KnownFields behavior.
func decodeGPUEntry(node *yaml.Node) (gpuFileEntry, error) {
	if node.Kind != yaml.MappingNode {
		return gpuFileEntry{}, errors.New("gpus entry must be a mapping with uuid, serial or overrides")
	}

	for i := 0; i < len(node.Content); i += 2 {
		if key := node.Content[i].Value; key != "uuid" && key != "serial" && key != "overrides" {
			return gpuFileEntry{}, fmt.Errorf("unknown gpus entry key %q, want uuid, serial or overrides", key)
		}
	}

	var spec struct {
		UUID      string                   `yaml:"uuid"`
		Serial    string                   `yaml:"serial"`
		Overrides map[string]overrideEntry `yaml:"overrides"`
	}

	if err := node.Decode(&spec); err != nil {
		return gpuFileEntry{}, fmt.Errorf("failed to decode gpus entry: %w", err)
	}

	return gpuFileEntry{uuid: spec.UUID, serial: spec.Serial, overrides: spec.Overrides}, nil
}

// validateGPUCount rejects a count the bus-id scheme cannot represent.
func validateGPUCount(count int) error {
	if count < 1 || count > maxGPUs {
		return fmt.Errorf("gpu count must be between 1 and %d, got %d", maxGPUs, count)
	}

	return nil
}

// resolveGPUs merges the --gpus flag with the config's gpus setting into the
// final identity list. The flag wins entirely: its generated identities
// replace any per-GPU config entries, including their overrides. A nil result
// means multi-GPU is off. The returned entries carry the per-GPU overrides
// for buildOverrides.
func resolveGPUs(raw rawFlags, fc *fileConfig) ([]gpuIdentity, []gpuFileEntry, error) {
	var (
		entries []gpuFileEntry
		count   int
	)

	switch {
	case raw.gpusSet:
		count = raw.gpus
	case fc != nil && fc.GPUs != nil:
		count, entries = fc.GPUs.count, fc.GPUs.entries
		if count == 0 {
			count = len(entries)
		}
	default:
		return nil, nil, nil
	}

	identities, err := buildIdentities(count, entries)
	if err != nil {
		return nil, nil, err
	}

	return identities, entries, nil
}

// buildIdentities resolves each GPU's identity: an explicit uuid/serial from
// its entry when given, a generated stable uuid otherwise. Explicit uuids
// must stay distinct after the exporter's own normalization, or all series
// would collapse into one.
func buildIdentities(count int, entries []gpuFileEntry) ([]gpuIdentity, error) {
	identities := make([]gpuIdentity, count)
	seen := make(map[string]int, count)

	for index := range identities {
		id := gpuIdentity{uuid: generatedUUID(index)}

		if index < len(entries) {
			if entries[index].uuid != "" {
				id.uuid = entries[index].uuid
			}

			id.serial = entries[index].serial
		}

		if err := validateSetValue("uuid", id.uuid); err != nil {
			return nil, err
		}

		if err := validateSetValue("serial", id.serial); err != nil {
			return nil, err
		}

		normalized := nvidiasmi.NormalizeUUID(id.uuid)
		if previous, duplicate := seen[normalized]; duplicate {
			return nil, fmt.Errorf("gpus %d and %d share uuid %q", previous, index, id.uuid)
		}

		seen[normalized] = index
		identities[index] = id
	}

	return identities, nil
}

// generatedUUID derives GPU i's stable uuid from a fixed namespace plus the
// index — never from the run seed. Metric values are intentionally
// non-reproducible across runs, but the identity must be reproducible, or
// Prometheus would see a brand new series on every scrape. The RFC 4122
// version/variant bits keep the shape valid for tooling that checks it.
func generatedUUID(index int) string {
	sum := sha256.Sum256(fmt.Appendf(nil, "nvidia_gpu_exporter/fake-nvidia-smi/gpu/%d", index))
	sum[6] = sum[6]&0x0f | 0x40
	sum[8] = sum[8]&0x3f | 0x80

	return fmt.Sprintf("GPU-%x-%x-%x-%x-%x", sum[0:4], sum[4:6], sum[6:8], sum[8:10], sum[10:16])
}

// rewriteIdentity stamps GPU index's identity onto one data row. Only the
// columns the capture actually records are touched, so a minimal hand-written
// capture keeps working; both the query-gpu field names and the compute-apps
// gpu_* spellings are covered, and a section only carries one of the two
// sets.
func rewriteIdentity(cells []string, columnOf map[string]int, id gpuIdentity, index, total int) {
	set := func(field, value string) {
		if column, ok := columnOf[field]; ok {
			cells[column] = value
		}
	}

	set("uuid", id.uuid)
	set("gpu_uuid", id.uuid)

	if id.serial != "" {
		set("serial", id.serial)
		set("gpu_serial", id.serial)
	}

	set("index", strconv.Itoa(index))
	set("count", strconv.Itoa(total))
	set("pci.bus", fmt.Sprintf("0x%02X", index+1))

	for _, field := range []string{"pci.bus_id", "gpu_bus_id"} {
		if column, ok := columnOf[field]; ok {
			cells[column] = rewriteBusID(cells[column], index+1)
		}
	}
}

// rewriteBusID replaces the bus byte of a captured PCI bus id (e.g.
// "00000000:0C:00.0"), keeping the captured domain, device and function so
// the id stays in the card's recorded shape; an unrecognized shape is
// replaced with a canonical id wholesale. Uppercase hex matches the corpus.
func rewriteBusID(captured string, bus int) string {
	parts := strings.Split(captured, ":")
	if len(parts) != 3 {
		return fmt.Sprintf("00000000:%02X:00.0", bus)
	}

	parts[1] = fmt.Sprintf("%02X", bus)

	return strings.Join(parts, ":")
}

// rejectIdentityOverrides refuses any override that targets an identity field
// while gpus is active, from any layer (flags, top-level config, per-GPU
// entries). Without gpus, identity fields stay overridable as before.
func rejectIdentityOverrides(ops []rawOverride, entries []gpuFileEntry) error {
	for _, op := range ops {
		if _, reserved := reservedIdentityFields[op.field]; reserved {
			return fmt.Errorf("field %q is a per-GPU identity and cannot be overridden while gpus is active", op.field)
		}
	}

	for _, entry := range entries {
		for field := range entry.overrides {
			if _, reserved := reservedIdentityFields[field]; reserved {
				return fmt.Errorf("field %q is a per-GPU identity and cannot be overridden while gpus is active", field)
			}
		}
	}

	return nil
}
