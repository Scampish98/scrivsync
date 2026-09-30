package yandex

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"path"
)

func (d *Client) Stat(ctx context.Context, p string) (*Resource, error) {
	var r Resource
	code, err := d.api(ctx, "GET", "/resources", url.Values{"path": {p}}, &r)
	if code == 404 {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if r.Type != "dir" && r.Type != "file" {
		return nil, errors.New("API не вернул тип ресурса")
	}
	if r.Type == "file" && (len(r.SHA256) != 64 || r.Size < 0) {
		return nil, errors.New("API не вернул SHA-256/размер файла; безопасная проверка невозможна")
	}
	return &r, nil
}

func (d *Client) Mkdir(ctx context.Context, p string) error {
	if p == "disk:" || p == "disk:/" {
		return nil
	}

	r, err := d.Stat(ctx, p)
	if err != nil {
		return err
	}
	if r != nil {
		if r.Type != "dir" {
			return fmt.Errorf("вместо папки существует файл: %s", p)
		}
		return nil
	}
	if err := d.Mkdir(ctx, path.Dir(p)); err != nil {
		return err
	}

	code, err := d.api(ctx, "PUT", "/resources", url.Values{"path": {p}}, nil)
	if code == 409 {
		r, e := d.Stat(ctx, p)
		if e == nil && r != nil && r.Type == "dir" {
			return nil
		}
	}

	return err
}
