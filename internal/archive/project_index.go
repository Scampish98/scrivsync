package archive

import (
	"archive/zip"
	"context"
	"errors"
	"io"
	"path"
	"strings"

	"scrivsync/internal/ctxio"
	"scrivsync/internal/fileutil"
	"scrivsync/internal/scrivx"
)

// ReadProjectIndex reads the single root .scrivx from the actual ZIP snapshot.
// Projects without a unique index keep ordinary conflict checking.
func ReadProjectIndex(ctx context.Context, filename string) (string, []byte, error) {
	r, err := zip.OpenReader(filename)
	if err != nil {
		return "", nil, err
	}
	defer fileutil.Close(r, "ZIP для базовой версии scrivx")
	if _, err := validateZIP(r.File); err != nil {
		return "", nil, err
	}
	var selected *zip.File
	for _, file := range r.File {
		_, relative, ok := strings.Cut(strings.TrimSuffix(file.Name, "/"), "/")
		if !ok || file.FileInfo().IsDir() || path.Dir(relative) != "." || path.Ext(relative) != ".scrivx" {
			continue
		}
		if selected != nil {
			return "", nil, nil
		}
		selected = file
	}
	if selected == nil {
		return "", nil, nil
	}
	_, relative, _ := strings.Cut(selected.Name, "/")
	if selected.UncompressedSize64 > scrivx.MaxSize {
		return relative, nil, nil
	}
	opened, err := selected.Open()
	if err != nil {
		return "", nil, err
	}
	data, readErr := io.ReadAll(ctxio.Reader{Context: ctx, Source: io.LimitReader(opened, scrivx.MaxSize+1)})
	if err := errors.Join(readErr, opened.Close()); err != nil {
		return "", nil, err
	}
	return relative, data, nil
}
