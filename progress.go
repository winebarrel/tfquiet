package tfquiet

import (
	"fmt"
	"io"
	"regexp"
	"strings"
	"sync"
	"time"
)

var spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

const progressInterval = 100 * time.Millisecond

// noiseLabelRe extracts the action verb from a noise line for use as the
// spinner label. The capture mirrors the alternatives in noiseLineRe but
// strips the trailing space/bracket so the label reads cleanly.
var noiseLabelRe = regexp.MustCompile(`: (Refreshing state\.\.\.|Preparing import\.\.\.|Reading\.\.\.|Read complete after\b|Opening\.\.\.|Opening complete after\b|Closing\.\.\.|Closing complete after\b|Still [a-z]+\.\.\.)`)

type progressTracker struct {
	w        io.Writer
	interval time.Duration

	mu      sync.Mutex
	count   int
	label   string
	spinIdx int
	active  bool
	stopped bool

	stopCh chan struct{}
	doneCh chan struct{}
}

func newProgressTracker(w io.Writer) *progressTracker {
	if w == nil {
		return nil
	}
	p := &progressTracker{
		w:        w,
		interval: progressInterval,
		label:    "Filtering",
		stopCh:   make(chan struct{}),
		doneCh:   make(chan struct{}),
	}
	go p.loop()
	return p
}

func (p *progressTracker) loop() {
	defer close(p.doneCh)
	ticker := time.NewTicker(p.interval)
	defer ticker.Stop()
	for {
		select {
		case <-p.stopCh:
			return
		case <-ticker.C:
			p.draw()
		}
	}
}

func (p *progressTracker) draw() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.stopped || p.count == 0 {
		return
	}
	p.spinIdx = (p.spinIdx + 1) % len(spinnerFrames)
	fmt.Fprintf(p.w, "\r\x1b[2K%s %s (%d)", spinnerFrames[p.spinIdx], p.label, p.count)
	p.active = true
}

func (p *progressTracker) tick(line string) {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.stopped {
		return
	}
	p.count++
	if l := extractNoiseLabel(line); l != "" {
		p.label = l
	}
}

// finish stops the spinner loop and erases the spinner line (if any). Safe
// to call more than once — subsequent calls are no-ops. Filter calls finish
// at end of input and also as soon as the first real line is emitted, so the
// spinner disappears the moment real plan output starts streaming.
func (p *progressTracker) finish() {
	if p == nil {
		return
	}
	p.mu.Lock()
	if p.stopped {
		p.mu.Unlock()
		return
	}
	p.stopped = true
	p.mu.Unlock()

	close(p.stopCh)
	<-p.doneCh

	p.mu.Lock()
	defer p.mu.Unlock()
	if p.active {
		fmt.Fprint(p.w, "\r\x1b[2K")
		p.active = false
	}
}

func extractNoiseLabel(s string) string {
	m := noiseLabelRe.FindStringSubmatch(s)
	if m != nil {
		return strings.TrimSpace(m[1])
	}
	if lockLineRe.MatchString(s) {
		return "Acquiring state lock"
	}
	return ""
}
