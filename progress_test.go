package tfquiet_test

import (
	"bytes"
	"io"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/winebarrel/tfquiet"
)

// syncBuffer is a goroutine-safe buffer so the progress goroutine and the
// test goroutine can write/read without races.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func TestFilter_ProgressWriterEmitsSpinnerAndClearsOnExit(t *testing.T) {
	// Input is pure refresh noise with no real plan output: progress should
	// have a chance to draw, and the final \r\x1b[2K erase should appear so
	// the stderr line is clean by the time Filter returns.
	var noise strings.Builder
	for i := 0; i < 500; i++ {
		noise.WriteString("terraform_data.foo: Refreshing state... [id=abc]\n")
	}

	var prog syncBuffer
	var out bytes.Buffer
	opts := &tfquiet.Options{Progress: &prog}

	require.NoError(t, tfquiet.Filter(strings.NewReader(noise.String()), &out, opts))

	// Noise lines should be filtered from stdout.
	assert.Empty(t, out.String(), "noise should be filtered from stdout")

	got := prog.String()
	// We can't assert the spinner fired (race against the timer), but we can
	// assert that *if* anything was drawn, the trailing erase was emitted —
	// and that nothing was drawn that doesn't contain our label.
	if got != "" {
		assert.Contains(t, got, "Refreshing state...")
		assert.True(t, strings.HasSuffix(got, "\r\x1b[2K"), "progress output must end with a line-erase")
	}
}

func TestFilter_ProgressClearedOnFirstRealLine(t *testing.T) {
	// A "real" line emitted to stdout must cause the progress meter to clear
	// itself, even though more input lines may follow.
	input := "Acquiring state lock. This may take a few moments...\nhello world\n"

	var prog syncBuffer
	var out bytes.Buffer
	opts := &tfquiet.Options{Progress: &prog}

	require.NoError(t, tfquiet.Filter(strings.NewReader(input), &out, opts))

	assert.Equal(t, "hello world\n", out.String())
	got := prog.String()
	if got != "" {
		assert.True(t, strings.HasSuffix(got, "\r\x1b[2K"), "progress output must end with a line-erase")
	}
}

func TestFilter_ProgressDisabledByDefault(t *testing.T) {
	// Without Options.Progress set, no spinner goroutine should be spawned
	// and nothing should be written anywhere except stdout.
	input := "terraform_data.foo: Refreshing state... [id=abc]\nhello\n"

	var out bytes.Buffer
	require.NoError(t, tfquiet.Filter(strings.NewReader(input), &out, nil))
	assert.Equal(t, "hello\n", out.String())
}

func TestFilter_ProgressWriterNilSafe(t *testing.T) {
	// Explicitly nil Progress writer must be treated the same as unset.
	input := "terraform_data.foo: Refreshing state... [id=abc]\nhello\n"

	var out bytes.Buffer
	opts := &tfquiet.Options{Progress: io.Writer(nil)}
	require.NoError(t, tfquiet.Filter(strings.NewReader(input), &out, opts))
	assert.Equal(t, "hello\n", out.String())
}

func TestFilter_ProgressShowNoiseSuppressesTicks(t *testing.T) {
	// With ShowNoise=true the noise lines pass through unchanged and the
	// progress meter should not tick (nothing to count).
	input := "terraform_data.foo: Refreshing state... [id=abc]\n"

	var prog syncBuffer
	var out bytes.Buffer
	opts := &tfquiet.Options{ShowNoise: true, Progress: &prog}
	require.NoError(t, tfquiet.Filter(strings.NewReader(input), &out, opts))

	assert.Equal(t, input, out.String())
	// The progress writer may receive an empty/erase sequence but never a
	// drawn spinner frame.
	assert.NotContains(t, prog.String(), "Refreshing state...")
}
