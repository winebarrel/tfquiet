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
	importWillBeRe  = regexp.MustCompile(`^ {1,2}# .+ will be imported$`)
	noiseLineRe     = regexp.MustCompile(`: (Refreshing state\.\.\.|Preparing import\.\.\.|Reading\.\.\.|Read complete after |Opening\.\.\.|Opening complete after |Closing\.\.\.|Closing complete after )`)
	lockLineRe      = regexp.MustCompile(`^Acquiring state lock\.`)
	dividerRe       = regexp.MustCompile(`^─+$`)
	noteFooterRe    = regexp.MustCompile(`^Note: You didn't use the -out option`)
	warningStartRe  = regexp.MustCompile(`^Warning: Some objects will no longer be managed by Terraform`)
	driftStartRe    = regexp.MustCompile(`^Note: Objects have changed outside of Terraform`)
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
}

type streamFilter struct {
	w    io.Writer
	opts *Options

	block            *block
	blockReadingBody bool

	pendingBlanks     int
	sawNonBlank       bool
	skipNextBlank     bool
	skippingWarning   bool
	skippingDrift     bool
	sawDriftStart     bool
	passedDriftCloser bool
	done              bool
}

func (f *streamFilter) processLine(line string) error {
	s := stripANSI(line)

	if f.block != nil {
		return f.continueBlock(line, s)
	}

	// Inside a drift section we're hiding: consume everything until the
	// closing divider. The blank that typically follows the divider also
	// belongs to the drift block.
	if f.skippingDrift {
		if dividerRe.MatchString(s) {
			f.skippingDrift = false
			f.passedDriftCloser = true
			f.skipNextBlank = true
		}
		return nil
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

	// Drift section start. Always remember we saw it, so the divider that
	// closes the section can be recognized as a separator (not end-of-plan).
	if driftStartRe.MatchString(s) {
		f.sawDriftStart = true
		if !f.opts.ShowDrift {
			f.skippingDrift = true
			return nil
		}
		// --show-drift: fall through to emit the line.
	}

	if !f.opts.ShowNoise && noteFooterRe.MatchString(s) {
		f.done = true
		return nil
	}

	if !f.opts.ShowNoise && dividerRe.MatchString(s) {
		if f.sawDriftStart && !f.passedDriftCloser {
			// Divider separating the drift section from the actions
			// section. With --show-drift this is the visual boundary;
			// emit it. Either way, don't terminate here.
			f.passedDriftCloser = true
			return f.emit(line)
		}
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

	return f.emit(line)
}

func (f *streamFilter) classifyHeader(s string) {
	switch {
	case importWillBeRe.MatchString(s) && f.block.kind == kindOther:
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
			// Policy: if the resource line carries a real change marker
			// (~ / + / - / -/+ / +/-), the block must be shown even if its
			// header looked like a filterable kind (e.g. an `import {}` that
			// also updates the resource in-place). Drop only when the prefix
			// indicates "no real resource change" — 4 spaces (pure import /
			// pure move) or " . " (state-only forget).
			if hasResourceChange(s) {
				b.kind = kindOther
			}
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

// hasResourceChange reports whether the resource header line indicates an
// actual change (~, +, -, -/+, +/-). Prefixes "    " (pure import / pure
// move) and " . " (state-only forget) mean no real change.
func hasResourceChange(resourceLine string) bool {
	switch {
	case strings.HasPrefix(resourceLine, "    "),
		strings.HasPrefix(resourceLine, " . "):
		return false
	}
	return true
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

