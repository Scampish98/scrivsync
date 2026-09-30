package yandex

import (
	"bytes"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

type failingResponseBody struct{ io.Reader }

func (failingResponseBody) Close() error {
	return &url.Error{Op: "Close", URL: "https://uploader.disk.yandex.net/?token=secret", Err: errors.New("connection reset")}
}

func TestResponseCloseFailureDoesNotLeakSignedURL(t *testing.T) {
	var output bytes.Buffer
	original := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&output, nil)))
	t.Cleanup(func() { slog.SetDefault(original) })

	closeResponse(&http.Response{Body: failingResponseBody{strings.NewReader("")}})

	logged := output.String()
	if !strings.Contains(logged, "connection reset") {
		t.Fatalf("close error not logged: %s", logged)
	}
	if strings.Contains(logged, "secret") || strings.Contains(logged, "https://") {
		t.Fatalf("signed URL leaked: %s", logged)
	}
}
