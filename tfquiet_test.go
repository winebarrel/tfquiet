package tfquiet_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/winebarrel/tfquiet"
	"go.yaml.in/yaml/v4"
)

type testCase struct {
	Name         string `yaml:"name"`
	Input        string `yaml:"input"`
	InputFile    string `yaml:"inputFile"`
	Expected     string `yaml:"expected"`
	ExpectedFile string `yaml:"expectedFile"`
	ShowMoved    bool   `yaml:"showMoved"`
	ShowImport   bool   `yaml:"showImport"`
	ShowRemoved  bool   `yaml:"showRemoved"`
	ShowNoise    bool   `yaml:"showNoise"`
}

func TestFilter(t *testing.T) {
	files, err := filepath.Glob("testdata/*.yml")
	require.NoError(t, err)
	require.NotEmpty(t, files)

	for _, f := range files {
		yml, err := os.ReadFile(f)
		require.NoError(t, err)

		var tests []testCase
		require.NoError(t, yaml.Unmarshal(yml, &tests))

		for _, tt := range tests {
			t.Run(f+"/"+tt.Name, func(t *testing.T) {
				input := tt.Input
				if tt.InputFile != "" {
					b, err := os.ReadFile(filepath.Join("testdata", tt.InputFile))
					require.NoError(t, err)
					input = string(b)
				}

				expected := tt.Expected
				if tt.ExpectedFile != "" {
					b, err := os.ReadFile(filepath.Join("testdata", tt.ExpectedFile))
					require.NoError(t, err)
					expected = string(b)
				}

				opts := &tfquiet.Options{
					ShowMoved:   tt.ShowMoved,
					ShowImport:  tt.ShowImport,
					ShowRemoved: tt.ShowRemoved,
					ShowNoise:   tt.ShowNoise,
				}

				out, err := tfquiet.FilterBytes([]byte(input), opts)
				require.NoError(t, err)
				assert.Equal(t, expected, string(out))
			})
		}
	}
}

func TestFilter_NilOptionsTreatedAsDefault(t *testing.T) {
	out, err := tfquiet.FilterBytes([]byte("hello\n"), nil)
	require.NoError(t, err)
	assert.Equal(t, "hello\n", string(out))
}

type errReader struct{ err error }

func (r *errReader) Read(p []byte) (int, error) { return 0, r.err }

func TestFilter_PropagatesReaderError(t *testing.T) {
	_, err := tfquiet.Filter(&errReader{err: errors.New("boom")}, nil)
	require.ErrorContains(t, err, "boom")
	require.ErrorContains(t, err, "failed to read input")
}
