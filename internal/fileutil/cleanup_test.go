package fileutil

import (
	"bytes"
	"errors"
	"log/slog"
	"strings"
	"testing"
)

type failingCloser struct{}

func (failingCloser) Close() error { return errors.New("simulated close failure") }

func TestCleanupFailureIsLogged(t *testing.T) {
	var output bytes.Buffer
	original := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&output, nil)))
	t.Cleanup(func() { slog.SetDefault(original) })

	Close(failingCloser{}, "test archive")

	for _, expected := range []string{"level=ERROR", "test archive", "simulated close failure"} {
		if !strings.Contains(output.String(), expected) {
			t.Fatalf("cleanup diagnostic missing %q: %s", expected, output.String())
		}
	}
}
