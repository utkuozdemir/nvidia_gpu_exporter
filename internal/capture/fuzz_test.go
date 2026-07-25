package capture_test

import (
	"testing"

	"github.com/utkuozdemir/nvidia_gpu_exporter/internal/capture"
)

const seedCapture = "################################################################################\n" +
	"# machine: test\n" +
	"################################################################################\n" +
	"\nsome metadata\n\n" +
	"################################################################################\n" +
	"# idle :: query-gpu (csv, what the exporter parses)\n" +
	"# $ nvidia-smi --query-gpu=uuid,name --format=csv\n" +
	"################################################################################\n" +
	"\nuuid, name\nGPU-abc, Tesla T4\n\n"

func FuzzParse(f *testing.F) {
	f.Add(seedCapture)
	f.Add("")
	f.Fuzz(func(t *testing.T, content string) {
		capt, err := capture.Parse(content)
		if err != nil {
			return
		}

		for index, section := range capt.Sections {
			// State and Label are how every consumer addresses a section; an
			// empty state makes a section unaddressable by Find.
			if section.State == "" {
				t.Fatalf("section %d parsed with an empty state", index)
			}

			// Every parsed section must be reachable through the accessor the
			// fake and the tests use. A section that parses but cannot be
			// looked up is a capture that silently serves nothing.
			if capt.Find(section.State, section.Label) == nil {
				t.Fatalf("section %d (%q :: %q) parsed but Find cannot reach it",
					index, section.State, section.Label)
			}
		}
	})
}
