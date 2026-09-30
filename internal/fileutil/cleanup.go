// Package fileutil reports cleanup failures that cannot be returned to the caller.
package fileutil

import (
	"io"
	"log/slog"
	"os"
)

func Close(c io.Closer, description string) {
	if err := c.Close(); err != nil {
		slog.Error("Не удалось закрыть ресурс", "resource", description, "error", err)
	}
}

// CloseFile consumes the handle even when Close fails, avoiding a second close.
func CloseFile(f **os.File) error {
	if *f == nil {
		return nil
	}

	opened := *f
	*f = nil
	return opened.Close()
}

func CloseFileOnReturn(f **os.File) {
	if *f == nil {
		return
	}

	name := (*f).Name()
	if err := CloseFile(f); err != nil {
		slog.Error("Не удалось закрыть файл", "path", name, "error", err)
	}
}

func Remove(path string) {
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		slog.Error("Не удалось удалить временный файл", "path", path, "error", err)
	}
}

func RemoveAll(path string) {
	if err := os.RemoveAll(path); err != nil {
		slog.Error("Не удалось удалить временную папку", "path", path, "error", err)
	}
}
