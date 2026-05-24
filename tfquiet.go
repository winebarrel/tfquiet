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
	var buf bytes.Buffer
	if err := Filter(bytes.NewReader(src), &buf, opts); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func Filter(r io.Reader, w io.Writer, opts *Options) error {
	if opts == nil {
		opts = &Options{}
	}

	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)

	f := &streamFilter{w: w, opts: opts}

	for sc.Scan() {
		if err := f.processLine(sc.Text()); err != nil {
			return err
		}
		if f.done {
			break
		}
	}

	if err := sc.Err(); err != nil {
		return fmt.Errorf("failed to read input: %w", err)
	}

	return f.finish()
}

var (
	ansiRe          = regexp.MustCompile(`\x1b\[[0-9;]*[a-zA-Z]`)
	blockHeaderRe   = regexp.MustCompile(`^ {1,2}# `)
	resourceLineRe  = regexp.MustCompile(`^( \. |[+\-~/ ]{4})resource "`)
	blockCloseRe    = regexp.MustCompile(`^    }$`)
	movedHeaderRe   = regexp.MustCompile(` has moved to `)
	removedHeaderRe = regexp.MustCompile(` will no longer be managed by Terraform`)
	importHeaderRe  = regexp.MustCompile(`^ {1,2}# \(imported from `)
	noiseLineRe     = regexp.MustCompile(`: (Refreshing state\.\.\.|Preparing import\.\.\.|Reading\.\.\.|Read complete after |Opening\.\.\.|Opening complete after |Closing\.\.\.|Closing complete after )`)
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

type streamFilter struct {
	w    io.Writer
	opts *Options

	dropped struct {
		nImport, nAdd, nChange, nDestroy int
	}

	block            *block
	blockReadingBody bool

	pendingBlanks   int
	sawNonBlank     bool
	skipNextBlank   bool
	skippingWarning bool
	done            bool
}

func (f *streamFilter) processLine(line string) error {
	s := stripANSI(line)

	if f.block != nil {
		return f.continueBlock(line, s)
	}

	// Any non-blank line consumes the "skip next blank" claim that was set
	// by a dropped block — even if the line itself is skipped (noise, divider,
	// warning start). skipNextBlank should only target a truly-adjacent blank.
	if s != "" {
		f.skipNextBlank = false
	}

	if f.skippingWarning {
		if dividerRe.MatchString(s) {
			f.skippingWarning = false
			// fall through and let the divider trigger the done path below
		} else {
			return nil
		}
	}

	if !f.opts.ShowNoise && isNoiseLine(s) {
		return nil
	}

	if !f.opts.ShowNoise && (dividerRe.MatchString(s) || noteFooterRe.MatchString(s)) {
		f.done = true
		return nil
	}

	if !f.opts.ShowRemoved && warningStartRe.MatchString(s) {
		f.skippingWarning = true
		return nil
	}

	if blockHeaderRe.MatchString(s) {
		f.block = &block{kind: kindOther}
		f.block.lines = append(f.block.lines, line)
		f.classifyHeader(s)
		return nil
	}

	if planSummaryRe.MatchString(s) {
		return f.emit(rewriteSummary(line, f.dropped.nImport, f.dropped.nAdd, f.dropped.nChange, f.dropped.nDestroy))
	}

	return f.emit(line)
}

func (f *streamFilter) classifyHeader(s string) {
	switch {
	case importHeaderRe.MatchString(s):
		f.block.kind = kindImport
	case movedHeaderRe.MatchString(s) && f.block.kind == kindOther:
		f.block.kind = kindMoved
	case removedHeaderRe.MatchString(s) && f.block.kind == kindOther:
		f.block.kind = kindRemoved
	}
}

func (f *streamFilter) continueBlock(line, s string) error {
	b := f.block

	if !f.blockReadingBody {
		if blockHeaderRe.MatchString(s) {
			b.lines = append(b.lines, line)
			f.classifyHeader(s)
			return nil
		}

		if resourceLineRe.MatchString(s) {
			b.lines = append(b.lines, line)
			classifyCount(b, s)
			f.blockReadingBody = true
			return nil
		}

		// Headers were not followed by a resource line — emit them and reprocess
		// the current line in idle state.
		for _, l := range b.lines {
			if err := f.emit(l); err != nil {
				return err
			}
		}
		f.block = nil
		return f.processLine(line)
	}

	b.lines = append(b.lines, line)
	if blockCloseRe.MatchString(s) {
		return f.flushBlock()
	}
	return nil
}

// finish handles a block left dangling at EOF. Both header-only and
// mid-body cases go through flushBlock so kind-based drop/keep stays
// consistent with the readBlock-then-shouldDrop flow from the original
// buffered implementation.
func (f *streamFilter) finish() error {
	if f.block == nil {
		return nil
	}
	return f.flushBlock()
}

func (f *streamFilter) flushBlock() error {
	b := f.block
	f.block = nil
	f.blockReadingBody = false

	if shouldDrop(b, f.opts) {
		f.dropped.nImport += b.nImport
		f.dropped.nAdd += b.nAdd
		f.dropped.nChange += b.nChange
		f.dropped.nDestroy += b.nDestroy
		f.skipNextBlank = true
		return nil
	}

	for _, l := range b.lines {
		if err := f.emit(l); err != nil {
			return err
		}
	}
	return nil
}

func (f *streamFilter) emit(line string) error {
	if line == "" {
		if !f.sawNonBlank {
			return nil
		}
		if f.skipNextBlank {
			f.skipNextBlank = false
			return nil
		}
		f.pendingBlanks++
		return nil
	}

	f.skipNextBlank = false

	out := make([]byte, 0, f.pendingBlanks+len(line)+1)
	for i := 0; i < f.pendingBlanks; i++ {
		out = append(out, '\n')
	}
	f.pendingBlanks = 0
	f.sawNonBlank = true
	out = append(out, line...)
	out = append(out, '\n')

	n, err := f.w.Write(out)
	if err == nil && n < len(out) {
		return io.ErrShortWrite
	}
	return err
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
