package tfquiet

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"regexp"
	"strings"
)

func FilterBytes(src []byte, opts *Options) ([]byte, error) {
	return Filter(bytes.NewReader(src), opts)
}

func Filter(r io.Reader, opts *Options) ([]byte, error) {
	if opts == nil {
		opts = &Options{}
	}

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
	ansiRe          = regexp.MustCompile(`\x1b\[[0-9;]*[a-zA-Z]`)
	blockHeaderRe   = regexp.MustCompile(`^ {1,2}# `)
	resourceLineRe  = regexp.MustCompile(`^( \. |[+\-~/ ]{4})resource "`)
	blockCloseRe    = regexp.MustCompile(`^    }$`)
	movedHeaderRe   = regexp.MustCompile(` has moved to `)
	removedHeaderRe = regexp.MustCompile(` will no longer be managed by Terraform`)
	importHeaderRe  = regexp.MustCompile(`^ {1,2}# \(imported from `)
	noiseLineRe     = regexp.MustCompile(`: (Refreshing state\.\.\.|Preparing import\.\.\.|Reading\.\.\.|Read complete after )`)
	lockLineRe      = regexp.MustCompile(`^Acquiring state lock\.`)
	dividerRe       = regexp.MustCompile(`^─+$`)
	planSummaryRe   = regexp.MustCompile(`^Plan: `)
	noteFooterRe    = regexp.MustCompile(`^Note: You didn't use the -out option`)
	warningStartRe  = regexp.MustCompile(`^Warning: Some objects will no longer be managed by Terraform`)
)

// stripANSI returns line with ANSI CSI escape sequences and trailing carriage
// returns removed, so regex matching can run against the visible text.
func stripANSI(line string) string {
	line = strings.TrimRight(line, "\r")
	if !strings.ContainsRune(line, '\x1b') {
		return line
	}
	return ansiRe.ReplaceAllString(line, "")
}

type blockKind int

const (
	kindOther blockKind = iota
	kindMoved
	kindImport
	kindRemoved
)

type block struct {
	kind  blockKind
	lines []string
	// counts contribution to "Plan: X to import, Y to add, Z to change, W to destroy."
	nImport, nAdd, nChange, nDestroy int
}

func filterLines(lines []string, opts *Options) []byte {
	stripped := make([]string, len(lines))
	for i, l := range lines {
		stripped[i] = stripANSI(l)
	}

	var dropped struct {
		nImport, nAdd, nChange, nDestroy int
	}

	out := &bytes.Buffer{}
	i := 0

	for i < len(lines) {
		s := stripped[i]

		if !opts.ShowNoise && isNoiseLine(s) {
			i++
			continue
		}

		if !opts.ShowNoise && (dividerRe.MatchString(s) || noteFooterRe.MatchString(s)) {
			// Drop divider / Note paragraph through EOF.
			break
		}

		if !opts.ShowRemoved && warningStartRe.MatchString(s) {
			// Skip the trailing "Warning: Some objects will no longer be
			// managed" section that pairs with removed{} destroy=false blocks.
			for i < len(lines) && !dividerRe.MatchString(stripped[i]) {
				i++
			}
			continue
		}

		if blockHeaderRe.MatchString(s) {
			b, next := readBlock(lines, stripped, i)
			if shouldDrop(b, opts) {
				dropped.nImport += b.nImport
				dropped.nAdd += b.nAdd
				dropped.nChange += b.nChange
				dropped.nDestroy += b.nDestroy
				// Also skip the trailing blank line after the block, if any.
				if next < len(lines) && stripped[next] == "" {
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

		if planSummaryRe.MatchString(s) {
			out.WriteString(rewriteSummary(lines[i], dropped.nImport, dropped.nAdd, dropped.nChange, dropped.nDestroy))
			out.WriteByte('\n')
			i++
			continue
		}

		out.WriteString(lines[i])
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

func isNoiseLine(s string) bool {
	if noiseLineRe.MatchString(s) {
		return true
	}
	if lockLineRe.MatchString(s) {
		return true
	}
	return false
}

func readBlock(lines, stripped []string, start int) (*block, int) {
	b := &block{kind: kindOther}
	i := start

	// Header comment lines.
	for i < len(lines) && blockHeaderRe.MatchString(stripped[i]) {
		b.lines = append(b.lines, lines[i])

		switch {
		case importHeaderRe.MatchString(stripped[i]):
			b.kind = kindImport
		case movedHeaderRe.MatchString(stripped[i]) && b.kind == kindOther:
			b.kind = kindMoved
		case removedHeaderRe.MatchString(stripped[i]) && b.kind == kindOther:
			b.kind = kindRemoved
		}

		i++
	}

	// Resource opening line.
	if i < len(lines) && resourceLineRe.MatchString(stripped[i]) {
		b.lines = append(b.lines, lines[i])
		classifyCount(b, stripped[i])
		i++
	} else {
		// Not a resource block we recognize. Mark as other and stop here.
		return b, i
	}

	// Body until closing line.
	for i < len(lines) {
		b.lines = append(b.lines, lines[i])
		if blockCloseRe.MatchString(stripped[i]) {
			i++
			break
		}
		i++
	}

	return b, i
}

func classifyCount(b *block, resourceLine string) {
	// resourceLine begins with one of: "  + ", "  ~ ", "  - ", "-/+ ", "+/- ", "    ", " . "
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

func shouldDrop(b *block, opts *Options) bool {
	switch b.kind {
	case kindMoved:
		return !opts.ShowMoved
	case kindImport:
		return !opts.ShowImport
	case kindRemoved:
		return !opts.ShowRemoved
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
