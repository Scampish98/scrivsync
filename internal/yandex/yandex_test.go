package yandex

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func fixtureYandex(t *testing.T, handler http.HandlerFunc) *Client {
	t.Helper()
	d := New("test-token")
	d.pollDelay = time.Nanosecond
	d.client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		w := httptest.NewRecorder()
		handler(w, r)
		return w.Result(), nil
	})
	return d
}

func TestYandexTransferLinksAuthAndNoOverwrite(t *testing.T) {
	var upload, download bool
	d := fixtureYandex(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Host == "cloud-api.yandex.net" {
			if r.Header.Get("Authorization") != "OAuth test-token" {
				t.Error("missing API auth")
			}
			if r.URL.Query().Get("path") != "disk:/Роман/Мой проект.zip" {
				t.Error("path encoding lost")
			}
			switch r.URL.Path {
			case "/v1/disk/resources/upload":
				if r.URL.Query().Get("overwrite") != "false" {
					t.Error("overwrite enabled")
				}
				json.NewEncoder(w).Encode(link{Href: "https://uploader.disk.yandex.net/upload?signed=secret", Method: "PUT"})
			case "/v1/disk/resources/download":
				json.NewEncoder(w).Encode(link{Href: "https://downloader.disk.yandex.ru/download?signed=secret", Method: "GET"})
			default:
				t.Errorf("unexpected API %s", r.URL.Path)
			}
			return
		}
		if r.Header.Get("Authorization") != "" {
			t.Error("OAuth token leaked to transfer host")
		}
		if r.Method == "PUT" {
			upload = true
			b, err := io.ReadAll(r.Body)
			if err != nil || string(b) != "zip bytes" {
				t.Error("bad upload")
			}
			if r.ContentLength != 9 {
				t.Error("missing content length")
			}
			w.WriteHeader(201)
		} else {
			download = true
			io.WriteString(w, "zip bytes")
		}
	})
	base := t.TempDir()
	src := filepath.Join(base, "in.zip")
	if err := os.WriteFile(src, []byte("zip bytes"), 0600); err != nil {
		t.Fatal(err)
	}
	remote := "disk:/Роман/Мой проект.zip"
	if err := d.Upload(context.Background(), src, remote); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(base, "out.zip")
	if err := d.Download(context.Background(), remote, dest); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(dest)
	if err != nil || string(b) != "zip bytes" || !upload || !download {
		t.Fatal("transfer failed")
	}
}

func TestYandexAsyncMove(t *testing.T) {
	polls := 0
	d := fixtureYandex(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "OAuth test-token" {
			t.Error("missing auth")
		}
		if r.Method == "POST" {
			if r.URL.Query().Get("from") != "disk:/a.zip" || r.URL.Query().Get("path") != "disk:/Backups/a.zip" || r.URL.Query().Get("overwrite") != "false" {
				t.Error("bad move arguments")
			}
			w.WriteHeader(202)
			json.NewEncoder(w).Encode(link{Href: "https://cloud-api.yandex.net/v1/disk/operations/123", Method: "GET"})
			return
		}
		polls++
		state := "in-progress"
		if polls == 2 {
			state = "success"
		}
		json.NewEncoder(w).Encode(map[string]string{"status": state})
	})
	if err := d.Move(context.Background(), "disk:/a.zip", "disk:/Backups/a.zip"); err != nil {
		t.Fatal(err)
	}
	if polls != 2 {
		t.Fatal("did not wait for operation completion")
	}
}

func TestYandexNotFoundIsDifferentFromPermissionDenied(t *testing.T) {
	for _, code := range []int{404, 401, 403, 429, 500} {
		t.Run(http.StatusText(code), func(t *testing.T) {
			d := fixtureYandex(t, func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(code)
				io.WriteString(w, "secret signed URL")
			})
			r, err := d.Stat(context.Background(), "disk:/a.zip")
			if code == 404 {
				if r != nil || err != nil {
					t.Fatal("404 not handled")
				}
			} else if err == nil || strings.Contains(err.Error(), "secret") {
				t.Fatalf("unsafe error handling: %v", err)
			}
		})
	}
}

func TestYandexRejectsForeignLinksAndSanitizesErrors(t *testing.T) {
	d := fixtureYandex(t, func(w http.ResponseWriter, r *http.Request) { t.Fatal("request should not be sent") })
	if _, err := d.request(context.Background(), "GET", "https://evil.example/file", nil, false); err == nil {
		t.Fatal("foreign transfer allowed")
	}
	if _, err := d.request(context.Background(), "GET", "https://evil.example/operation", nil, true); err == nil {
		t.Fatal("auth sent to foreign host")
	}
	err := safeNetworkError(&url.Error{Op: "Put", URL: "https://uploader.disk.yandex.net/upload?token=secret", Err: errors.New("connection reset")})
	if strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), "https://") {
		t.Fatal("signed URL leaked")
	}
}
