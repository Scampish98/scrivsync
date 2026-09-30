package sync

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"

	"scrivsync/internal/archive"
	"scrivsync/internal/report"
)

func (a *App) reportConflict(ctx context.Context, work string, local, incoming archive.Manifest, conflict error) error {
	projectDir := filepath.Join(work, "project")
	reportDir := filepath.Join(work, "report")
	a.log("Конфликт: скачанные данные сохранены в %q.", work)
	a.log("Архив: %s", filepath.Join(work, "project.zip"))
	a.log("Распакованный проект: %s", projectDir)

	err := report.Write(ctx, report.Options{
		RemotePath:  a.Remote,
		LocalDir:    a.Local,
		IncomingDir: projectDir,
		OutputDir:   reportDir,
		Local:       local,
		Incoming:    incoming,
		Conflict:    conflict,
	})
	if err != nil {
		return errors.Join(conflict, fmt.Errorf("не удалось завершить отчёт в %q; скачанные данные сохранены в %q: %w", reportDir, work, err))
	}

	a.log("Отчёт и список диффов: %s", filepath.Join(reportDir, "index.txt"))
	return conflict
}
