package tfquiet

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"regexp"
	"strings"
)

func FilterBytes(src []byte, optfns ...OptFn) ([]byte, error) {
	return Filter(bytes.NewReader(src), optfns...)
}

func Filter(r io.Reader, optfns ...OptFn) ([]byte, error) {
	opts := newOptions(optfns)

	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)

	var lines []string
	for sc.Scan() {
		lines = append(lines, sc.Text())
	}

	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("failed to read input: %w", err)
	}

	return filterLines(lines, opts), nil
}

var (
	blockHeaderRe   = regexp.MustCompile(`^  # `)
	resourceLineRe  = regexp.MustCompile(`^[+\-~/ ]{4}resource "`)
	blockCloseRe    = regexp.MustCompile(`^    }$`)
	movedHeaderRe   = regexp.MustCompile(` has moved to `)
	destroyHeaderRe = regexp.MustCompile(` will be destroyed$`)
	importHeaderRe  = regexp.MustCompile(`^  # \(imported from `)
	noiseLineRe     = regexp.MustCompile(`: (Refreshing state\.\.\.|Preparing import\.\.\.|Reading\.\.\.|Read complete after )`)
	lockLineRe      = regexp.MustCompile(`^Acquiring state lock\.`)
	dividerRe       = regexp.MustCompile(`^─+$`)
	planSummaryRe   = regexp.MustCompile(`^Plan: `)
)

type blockKind int

const (
	kindOther blockKind = iota
	kindMoved
	kindDestroy
	kindImport
)

type block struct {
	kind  blockKind
	lines []string
	// counts contribution to "Plan: X to import, Y to add, Z to change, W to destroy."
	nImport, nAdd, nChange, nDestroy int
}

func filterLines(lines []string, opts *options) []byte {
	var dropped struct {
		nImport, nAdd, nChange, nDestroy int
	}

	out := &bytes.Buffer{}
	i := 0

	for i < len(lines) {
		line := lines[i]

		if !opts.showNoise && isNoiseLine(line) {
			i++
			continue
		}

		if !opts.showNoise && isTrailingNoteStart(lines, i) {
			// Drop divider + Note paragraph through EOF.
			break
		}

		if blockHeaderRe.MatchString(line) {
			b, next := readBlock(lines, i)
			if shouldDrop(b, opts) {
				dropped.nImport += b.nImport
				dropped.nAdd += b.nAdd
				dropped.nChange += b.nChange
				dropped.nDestroy += b.nDestroy
				// Also skip the trailing blank line after the block, if any.
				if next < len(lines) && lines[next] == "" {
					next++
				}
				i = next
				continue
			}
			for _, l := range b.lines {
				out.WriteString(l)
				out.WriteByte('\n')
			}
			i = next
			continue
		}

		if planSummaryRe.MatchString(line) {
			out.WriteString(rewriteSummary(line, dropped.nImport, dropped.nAdd, dropped.nChange, dropped.nDestroy))
			out.WriteByte('\n')
			i++
			continue
		}

		out.WriteString(line)
		out.WriteByte('\n')
		i++
	}

	return trimLeadingBlanks(trimTrailingBlanks(out.Bytes()))
}

func trimLeadingBlanks(b []byte) []byte {
	for len(b) > 0 && b[0] == '\n' {
		b = b[1:]
	}
	return b
}

func isNoiseLine(line string) bool {
	if noiseLineRe.MatchString(line) {
		return true
	}
	if lockLineRe.MatchString(line) {
		return true
	}
	return false
}

func isTrailingNoteStart(lines []string, i int) bool {
	// A "Note:" footer is typically preceded by a horizontal divider.
	if dividerRe.MatchString(lines[i]) {
		return true
	}
	return false
}

func readBlock(lines []string, start int) (*block, int) {
	b := &block{kind: kindOther}
	i := start

	// Header comment lines.
	for i < len(lines) && blockHeaderRe.MatchString(lines[i]) {
		b.lines = append(b.lines, lines[i])

		switch {
		case importHeaderRe.MatchString(lines[i]):
			b.kind = kindImport
		case movedHeaderRe.MatchString(lines[i]) && b.kind == kindOther:
			b.kind = kindMoved
		case destroyHeaderRe.MatchString(lines[i]) && b.kind == kindOther:
			b.kind = kindDestroy
		}

		i++
	}

	// Resource opening line.
	if i < len(lines) && resourceLineRe.MatchString(lines[i]) {
		b.lines = append(b.lines, lines[i])
		classifyCount(b, lines[i])
		i++
	} else {
		// Not a resource block we recognize. Mark as other and stop here.
		return b, i
	}

	// Body until closing line.
	for i < len(lines) {
		b.lines = append(b.lines, lines[i])
		if blockCloseRe.MatchString(lines[i]) {
			i++
			break
		}
		i++
	}

	return b, i
}

func classifyCount(b *block, resourceLine string) {
	// resourceLine begins with one of: "  + ", "  ~ ", "  - ", "-/+ ", "+/- ", "    "
	prefix := resourceLine[:4]
	switch {
	case strings.HasPrefix(prefix, "-/+"), strings.HasPrefix(prefix, "+/-"):
		b.nAdd++
		b.nDestroy++
	case strings.HasPrefix(prefix, "  +"):
		b.nAdd++
	case strings.HasPrefix(prefix, "  -"):
		b.nDestroy++
	case strings.HasPrefix(prefix, "  ~"):
		b.nChange++
	}
	if b.kind == kindImport {
		b.nImport++
	}
}

func shouldDrop(b *block, opts *options) bool {
	switch b.kind {
	case kindMoved:
		return !opts.showMoved
	case kindDestroy:
		return !opts.showDestroy
	case kindImport:
		return !opts.showImport
	}
	return false
}

var summaryFieldRe = regexp.MustCompile(`(\d+) to (import|add|change|destroy)`)

func rewriteSummary(line string, dImport, dAdd, dChange, dDestroy int) string {
	if dImport == 0 && dAdd == 0 && dChange == 0 && dDestroy == 0 {
		return line
	}

	return summaryFieldRe.ReplaceAllStringFunc(line, func(match string) string {
		sm := summaryFieldRe.FindStringSubmatch(match)
		var n int
		fmt.Sscanf(sm[1], "%d", &n)
		switch sm[2] {
		case "import":
			n -= dImport
		case "add":
			n -= dAdd
		case "change":
			n -= dChange
		case "destroy":
			n -= dDestroy
		}
		if n < 0 {
			n = 0
		}
		return fmt.Sprintf("%d to %s", n, sm[2])
	})
}

func trimTrailingBlanks(b []byte) []byte {
	for len(b) > 0 && (b[len(b)-1] == '\n') {
		// Keep exactly one trailing newline.
		if len(b) >= 2 && b[len(b)-2] == '\n' {
			b = b[:len(b)-1]
			continue
		}
		break
	}
	return b
}
