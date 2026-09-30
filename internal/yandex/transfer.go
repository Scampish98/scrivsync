package yandex

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"os"

	"scrivsync/internal/ctxio"
	"scrivsync/internal/fileutil"
)

func (d *Client) Upload(ctx context.Context, local, remote string) error {
	var l link
	_, err := d.api(ctx, "GET", "/resources/upload", url.Values{"path": {remote}, "overwrite": {"false"}}, &l)
	if err != nil {
		return err
	}
	if l.Method != "PUT" || l.Href == "" {
		return errors.New("неверная ссылка загрузки от API")
	}

	f, err := os.Open(local)
	if err != nil {
		return err
	}

	defer fileutil.CloseFileOnReturn(&f)
	resp, err := d.request(ctx, "PUT", l.Href, uploadBody{f}, false)
	if err != nil {
		return err
	}

	defer closeResponse(resp)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return responseError(resp)
	}

	return nil // Caller polls metadata and verifies the complete file before rotating backups.
}

func (d *Client) Download(ctx context.Context, remote, local string) error {
	var l link
	_, err := d.api(ctx, "GET", "/resources/download", url.Values{"path": {remote}}, &l)
	if err != nil {
		return err
	}
	if l.Method != "GET" || l.Href == "" {
		return errors.New("неверная ссылка скачивания от API")
	}

	resp, err := d.request(ctx, "GET", l.Href, nil, false)
	if err != nil {
		return err
	}

	defer closeResponse(resp)
	if resp.StatusCode != 200 {
		return responseError(resp)
	}

	f, err := os.OpenFile(local, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}

	defer fileutil.CloseFileOnReturn(&f)
	if _, err := io.Copy(f, ctxio.Reader{Context: ctx, Source: resp.Body}); err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		return err
	}

	return fileutil.CloseFile(&f)
}

func (d *Client) Move(ctx context.Context, from, to string) error {
	var l link
	code, err := d.api(ctx, "POST", "/resources/move", url.Values{"from": {from}, "path": {to}, "overwrite": {"false"}}, &l)
	if err != nil {
		return err
	}
	if code != 202 {
		return nil
	}
	if l.Href == "" {
		return errors.New("API не вернул ссылку на незавершённую операцию")
	}

	for {
		if err := ctxio.Wait(ctx, d.pollDelay); err != nil {
			return err
		}
		status, err := d.operationStatus(ctx, l.Href)
		if err != nil {
			return err
		}

		switch status {
		case "success":
			return nil
		case "in-progress":
			continue
		case "failed":
			return errors.New("Яндекс Диск не выполнил перемещение")
		default:
			return errors.New("неизвестный статус операции Яндекс Диска")
		}
	}
}

// HTTP owns the request body; the Upload method owns the underlying file.
type uploadBody struct{ *os.File }

func (uploadBody) Close() error { return nil }

func (d *Client) operationStatus(ctx context.Context, target string) (string, error) {
	resp, err := d.request(ctx, "GET", target, nil, true)
	if err != nil {
		return "", err
	}

	defer closeResponse(resp)
	b, readErr := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != 200 {
		return "", responseError(resp)
	}
	if readErr != nil {
		return "", readErr
	}
	var state struct {
		Status string `json:"status"`
	}
	if err := json.NewDecoder(bytes.NewReader(b)).Decode(&state); err != nil {
		return "", errors.New("неверный ответ операции перемещения")
	}

	return state.Status, nil
}
