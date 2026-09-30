package yandex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

type Client struct {
	base, token string
	client      *http.Client
	pollDelay   time.Duration
	allowHTTP   bool
}

type link struct {
	Href   string `json:"href"`
	Method string `json:"method"`
}

func New(token string) *Client {
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.ResponseHeaderTimeout = 45 * time.Second
	return &Client{
		base:      "https://cloud-api.yandex.net/v1/disk",
		token:     token,
		pollDelay: time.Second,
		client: &http.Client{Transport: t, CheckRedirect: func(req *http.Request, via []*http.Request) error {
			// API redirects are unnecessary; signed transfer redirects may stay within Yandex.
			if len(via) >= 5 || via[0].Header.Get("Authorization") != "" || !validTransferURL(req.URL) {
				return errors.New("недопустимое перенаправление")
			}
			return nil
		}},
	}
}

func validTransferURL(u *url.URL) bool {
	if u.Scheme != "https" || u.User != nil || (u.Port() != "" && u.Port() != "443") {
		return false
	}

	h := strings.ToLower(u.Hostname())
	for _, domain := range []string{"yandex.ru", "yandex.net", "yandex.com"} {
		if h == domain || strings.HasSuffix(h, "."+domain) {
			return true
		}
	}

	return false
}

func safeNetworkError(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		return fmt.Errorf("сетевая ошибка: %w", ue.Err)
	}

	return err
}

func (d *Client) request(ctx context.Context, method, target string, body io.Reader, auth bool) (*http.Response, error) {
	u, err := url.Parse(target)
	if err != nil {
		return nil, errors.New("API вернул некорректный адрес")
	}
	base, err := url.Parse(d.base)
	if err != nil {
		return nil, errors.New("некорректный базовый адрес API")
	}
	if auth {
		if u.Scheme != base.Scheme || u.Host != base.Host || !strings.HasPrefix(u.Path, base.Path+"/") || u.User != nil {
			return nil, errors.New("отклонён посторонний адрес API")
		}
	} else if !validTransferURL(u) && !(d.allowHTTP && u.Scheme == base.Scheme && u.Host == base.Host) {
		return nil, errors.New("отклонён посторонний адрес передачи")
	}

	req, err := http.NewRequestWithContext(ctx, method, target, body)
	if err != nil {
		return nil, errors.New("не удалось создать HTTP-запрос")
	}
	if auth {
		req.Header.Set("Authorization", "OAuth "+d.token)
	}
	if f, ok := body.(interface{ Stat() (os.FileInfo, error) }); ok {
		fi, err := f.Stat()
		if err != nil {
			return nil, err
		}
		req.ContentLength = fi.Size()
		req.Header.Set("Content-Type", "application/zip")
	}

	resp, err := d.client.Do(req)
	if err != nil {
		return nil, safeNetworkError(err)
	}

	return resp, nil
}

func responseError(resp *http.Response) error {
	// Never print response bodies: they may contain signed URLs or authentication details.
	return fmt.Errorf("Яндекс Диск: HTTP %d (%s)", resp.StatusCode, http.StatusText(resp.StatusCode))
}

func (d *Client) api(ctx context.Context, method, endpoint string, q url.Values, result any) (int, error) {
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	target := d.base + endpoint
	if len(q) > 0 {
		target += "?" + q.Encode()
	}

	resp, err := d.request(ctx, method, target, nil, true)
	if err != nil {
		return 0, err
	}

	defer closeResponse(resp)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return resp.StatusCode, responseError(resp)
	}
	if result != nil {
		if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(result); err != nil {
			return resp.StatusCode, errors.New("некорректный JSON в ответе Яндекс Диска")
		}
	}

	return resp.StatusCode, nil
}

func closeResponse(resp *http.Response) {
	if err := resp.Body.Close(); err != nil {
		slog.Error("Не удалось закрыть HTTP-ответ", "error", safeNetworkError(err))
	}
}
