// Package capture parses the GPU capture files under testdata/captures: raw
// nvidia-smi output recorded from real machines, one self-contained file per
// machine/driver combination. The package knows the file format only; what
// the recorded commands mean is up to the caller.
package capture

import (
	"errors"
	"fmt"
	"os"
	"strings"
)

// fence delimits the blocks of a capture file.
const fence = "################################################################################"

// commentPrefix marks the heading lines between two fences.
const commentPrefix = "#"

// commandPrefix marks the heading line recording the command that produced
// the section body.
const commandPrefix = "# $ "

// stateSeparator splits a section heading into its state and label.
const stateSeparator = " :: "

var (
	errNoSections        = errors.New("no sections found")
	errUnterminatedBlock = errors.New("unterminated heading block")
)

// Section is one recorded command output within a capture file.
type Section struct {
	// State groups sections by what the machine was doing when they were
	// recorded, e.g. "capabilities", "idle" or "load".
	State string
	// Label names the section within its state.
	Label string
	// Command is the recorded command line that produced the body, without
	// the leading "$ ". Empty for the few derived sections no single command
	// produced.
	Command string
	// Body is the recorded output, without the blank lines that pad it in
	// the file.
	Body string
}

// Capture is a parsed capture file.
type Capture struct {
	// Header is the raw metadata block from the top of the file.
	Header string
	// Sections holds the recorded command outputs, in file order.
	Sections []Section
}

// Load reads and parses the capture file at path.
func Load(path string) (*Capture, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read capture file: %w", err)
	}

	parsed, err := Parse(string(data))
	if err != nil {
		return nil, fmt.Errorf("failed to parse capture file %q: %w", path, err)
	}

	return parsed, nil
}

// Parse parses capture file content. Blocks whose heading carries no
// state/label separator (the file's metadata header) are collected into
// Header; every other block becomes a Section.
func Parse(content string) (*Capture, error) {
	lines := strings.Split(content, "\n")
	capt := &Capture{}

	for lineNum := 0; lineNum < len(lines); {
		if lines[lineNum] != fence {
			if strings.TrimSpace(lines[lineNum]) != "" {
				return nil, fmt.Errorf("line %d: content outside any block: %q", lineNum+1, lines[lineNum])
			}

			lineNum++

			continue
		}

		heading, body, next, err := parseBlock(lines, lineNum)
		if err != nil {
			return nil, err
		}

		lineNum = next

		if err = capt.addBlock(heading, body); err != nil {
			return nil, err
		}
	}

	if len(capt.Sections) == 0 {
		return nil, errNoSections
	}

	return capt, nil
}

// parseBlock consumes one block starting at the opening fence at lines[start]:
// the heading lines up to the closing fence, then the body up to the next
// fence or the end of input. It returns the heading lines, the body with
// surrounding blank lines trimmed, and the index of the line after the body.
func parseBlock(lines []string, start int) ([]string, string, int, error) {
	lineNum := start + 1

	var heading []string

	for ; lineNum < len(lines) && lines[lineNum] != fence; lineNum++ {
		if !strings.HasPrefix(lines[lineNum], commentPrefix) {
			return nil, "", 0, fmt.Errorf("line %d: heading line without comment prefix: %q",
				lineNum+1, lines[lineNum])
		}

		heading = append(heading, lines[lineNum])
	}

	if lineNum == len(lines) {
		return nil, "", 0, errUnterminatedBlock
	}

	lineNum++ // the closing fence

	bodyStart := lineNum
	for lineNum < len(lines) && lines[lineNum] != fence {
		lineNum++
	}

	body := strings.Trim(strings.Join(lines[bodyStart:lineNum], "\n"), "\n")

	return heading, body, lineNum, nil
}

// Find returns the first section with the given state whose label starts with
// labelPrefix, or nil when there is none. Prefix matching, because labels
// carry free-form annotations after the name (e.g. "query-gpu (csv, what the
// exporter parses)").
func (c *Capture) Find(state, labelPrefix string) *Section {
	for i := range c.Sections {
		if c.Sections[i].State == state && strings.HasPrefix(c.Sections[i].Label, labelPrefix) {
			return &c.Sections[i]
		}
	}

	return nil
}

// addBlock files a parsed block as a section, or as (part of) the metadata
// header when its heading carries no state/label separator.
func (c *Capture) addBlock(heading []string, body string) error {
	if len(heading) == 0 {
		return errors.New("block with an empty heading")
	}

	title := strings.TrimSpace(strings.TrimPrefix(heading[0], commentPrefix))

	state, label, isSection := strings.Cut(title, stateSeparator)
	if !isSection {
		c.Header = strings.Trim(c.Header+"\n"+body, "\n")

		return nil
	}

	section := Section{
		State: strings.TrimSpace(state),
		Label: strings.TrimSpace(label),
		Body:  body,
	}

	for _, headingLine := range heading[1:] {
		if command, isCommand := strings.CutPrefix(headingLine, commandPrefix); isCommand {
			section.Command = strings.TrimSpace(command)

			break
		}
	}

	c.Sections = append(c.Sections, section)

	return nil
}
