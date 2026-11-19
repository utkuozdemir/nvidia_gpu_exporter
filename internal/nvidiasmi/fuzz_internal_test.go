package nvidiasmi

import (
	"regexp"
	"testing"
)

// FuzzFindSingleNumberMatchesRegex pins the allocation-free number scanner
// against the original numeric regex so both agree on every input.
func FuzzFindSingleNumberMatchesRegex(f *testing.F) {
	ref := regexp.MustCompile(`[+-]?(\d*[.])?\d+`)

	seeds := []string{
		"", "0", "42", "-5", "+5", "+.5", "-.5", "5.", "..5", "1..2",
		"+-5", "+.", "-.", "30.14 W", "8192 MiB", "1500 MHz", "P8",
		"123.456.789", "0X1234", "1e5", "[N/A]", "[Not Supported]",
		"aaa1234.56bbb", "aa111aa111", "0.00", "-0.00", "1.2.3",
	}
	for _, s := range seeds {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, input string) {
		got, gotOK := findSingleNumber(input)
		matches := ref.FindAllString(input, 2)
		wantOK := len(matches) == 1

		if gotOK != wantOK {
			t.Fatalf("input %q: got ok=%v (%q), want ok=%v (matches=%q)", input, gotOK, got, wantOK, matches)
		}

		if wantOK && got != matches[0] {
			t.Fatalf("input %q: got %q, want %q", input, got, matches[0])
		}
	})
}
