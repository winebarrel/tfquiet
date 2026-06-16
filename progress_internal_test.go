package tfquiet

import (
	"bytes"
	"io"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// syncWriter is a goroutine-safe writer; the progress loop runs concurrently
// with the test goroutine so direct bytes.Buffer access would race.
type syncWriter struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (w *syncWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.Write(p)
}

func (w *syncWriter) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.String()
}

// newManualTracker returns a tracker whose loop goroutine is not started, so
// tests can drive draw()/tick()/finish() deterministically.
func newManualTracker(w io.Writer) *progressTracker {
	return &progressTracker{
		w:        w,
		interval: time.Hour, // unused — loop is not started
		label:    "Filtering",
		stopCh:   make(chan struct{}),
		doneCh:   make(chan struct{}),
	}
}

func TestIsNilWriter(t *testing.T) {
	assert.True(t, isNilWriter(nil), "untyped nil")

	var f *os.File
	assert.True(t, isNilWriter(f), "typed-nil *os.File")

	var b *bytes.Buffer
	assert.True(t, isNilWriter(b), "typed-nil *bytes.Buffer")

	assert.False(t, isNilWriter(&bytes.Buffer{}), "real buffer")
	assert.False(t, isNilWriter(io.Discard), "io.Discard")
}

func TestNewProgressTracker_TypedNilReturnsNil(t *testing.T) {
	var f *os.File
	assert.Nil(t, newProgressTracker(f), "typed-nil writer must yield a nil tracker")
}

func TestProgressTracker_DrawSkipsWhenCountZero(t *testing.T) {
	var w syncWriter
	p := newManualTracker(&w)
	p.draw()
	assert.Empty(t, w.String(), "draw must not write before any tick")
}

func TestProgressTracker_DrawWritesFrameAndCount(t *testing.T) {
	var w syncWriter
	p := newManualTracker(&w)
	p.tick("terraform_data.foo: Refreshing state... [id=abc]")
	p.tick("terraform_data.bar: Refreshing state... [id=def]")
	p.draw()

	got := w.String()
	assert.Contains(t, got, "Refreshing state...")
	assert.Contains(t, got, "(2)")
	assert.True(t, strings.HasPrefix(got, "\r\x1b[2K"), "frame must start with line-erase: %q", got)
}

func TestProgressTracker_DrawAdvancesSpinnerFrames(t *testing.T) {
	var w syncWriter
	p := newManualTracker(&w)
	p.tick("Refreshing state...") // bumps count so draw renders
	for range spinnerFrames {
		p.draw()
	}
	got := w.String()
	for _, frame := range spinnerFrames {
		assert.Contains(t, got, frame)
	}
}

func TestProgressTracker_DrawSkipsAfterStop(t *testing.T) {
	var w syncWriter
	p := newManualTracker(&w)
	p.tick("Refreshing state...")

	// Simulate stopped without going through finish (no loop goroutine here).
	p.mu.Lock()
	p.stopped = true
	p.mu.Unlock()

	p.draw()
	assert.Empty(t, w.String(), "draw must not write after stop")
}

func TestProgressTracker_TickIgnoredAfterStop(t *testing.T) {
	p := newManualTracker(io.Discard)
	p.mu.Lock()
	p.stopped = true
	p.mu.Unlock()

	p.tick("Refreshing state...")
	p.mu.Lock()
	defer p.mu.Unlock()
	assert.Equal(t, 0, p.count, "tick after stop must not increment the counter")
}

func TestProgressTracker_TickKeepsLabelWhenNoMatch(t *testing.T) {
	p := newManualTracker(io.Discard)
	p.tick("nothing to extract here")
	p.mu.Lock()
	defer p.mu.Unlock()
	assert.Equal(t, "Filtering", p.label, "label must not be overwritten when no noise verb matches")
	assert.Equal(t, 1, p.count, "count must still bump")
}

func TestProgressTracker_FinishOnNilIsNoop(t *testing.T) {
	var p *progressTracker
	assert.NotPanics(t, func() { p.finish() })
	assert.NotPanics(t, func() { p.tick("x") })
}

func TestProgressTracker_FinishIsIdempotent(t *testing.T) {
	var w syncWriter
	p := newProgressTracker(&w)
	p.tick("Refreshing state...")
	p.finish()
	// Second finish must not deadlock on the already-closed stopCh.
	assert.NotPanics(t, func() { p.finish() })
}

func TestProgressTracker_FinishWithoutActiveDoesNotEraseAgain(t *testing.T) {
	var w syncWriter
	p := newProgressTracker(&w)
	// No tick → no draw → active stays false → finish must not write an
	// erase sequence (would leave a stray carriage return otherwise).
	p.finish()
	assert.Empty(t, w.String())
}

func TestExtractNoiseLabel(t *testing.T) {
	cases := map[string]string{
		"terraform_data.foo: Refreshing state... [id=abc]":          "Refreshing state...",
		"terraform_data.foo: Preparing import... [id=stub]":         "Preparing import...",
		"data.foo.bar: Reading...":                                  "Reading...",
		"data.foo.bar: Read complete after 1s":                      "Read complete after",
		"ephemeral.foo: Opening...":                                 "Opening...",
		"ephemeral.foo: Opening complete after 2s":                  "Opening complete after",
		"ephemeral.foo: Closing...":                                 "Closing...",
		"ephemeral.foo: Closing complete after 2s":                  "Closing complete after",
		"terraform_data.foo: Still refreshing... [10s elapsed]":     "Still refreshing...",
		"Acquiring state lock. This may take a few moments...":      "Acquiring state lock",
		"plain line with no marker":                                 "",
	}
	for in, want := range cases {
		assert.Equal(t, want, extractNoiseLabel(in), in)
	}
}
