package sync

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"

	"scrivsync/internal/archive"
	"scrivsync/internal/fileutil"
	"scrivsync/internal/yandex"
)

type Journal struct {
	Version         int              `json:"version"`
	Command         string           `json:"command"`
	Phase           string           `json:"phase"`
	Local           string           `json:"local"`
	Remote          string           `json:"remote"`
	Work            string           `json:"work"`
	BaselineUpdate  bool             `json:"baseline_update,omitempty"`
	Baseline        *baselineMeta    `json:"baseline,omitempty"`
	RetainWork      bool             `json:"retain_work,omitempty"`
	ExcludedBackup  string           `json:"excluded_backup,omitempty"`
	ExcludedFiles   archive.Manifest `json:"excluded_files,omitempty"`
	Backup          string           `json:"backup,omitempty"`
	TemporaryRemote string           `json:"temporary_remote,omitempty"`
	OldRemote       *yandex.Resource `json:"old_remote,omitempty"`
	ArchiveHash     string           `json:"archive_sha256,omitempty"`
	ArchiveSize     int64            `json:"archive_size,omitempty"`
	OldLocal        archive.Manifest `json:"old_local,omitempty"`
	NewLocal        archive.Manifest `json:"new_local,omitempty"`
}

// Temporary rollback data lives inside the operation directory and is cleaned up
// only after installation succeeds. Older journals keep their permanent backups.
func (j *Journal) temporaryPullBackup() bool {
	return j.Command == "pull" && j.Backup == filepath.Join(j.Work, "previous-project")
}

func (a *App) journalPath() string { return filepath.Join(a.State, "operation.json") }

func (a *App) save(j *Journal) error {
	b, err := json.MarshalIndent(j, "", "  ")
	if err != nil {
		return err
	}

	f, err := os.CreateTemp(a.State, "journal-")
	if err != nil {
		return err
	}

	tmp := f.Name()
	defer fileutil.Remove(tmp)
	defer fileutil.CloseFileOnReturn(&f)
	if _, err := f.Write(append(b, '\n')); err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		return err
	}
	if err := fileutil.CloseFile(&f); err != nil {
		return err
	}
	if err := os.Rename(tmp, a.journalPath()); err != nil {
		return err
	}

	return fileutil.SyncDir(a.State)
}

func (a *App) finish(j *Journal) error {
	if err := os.Remove(a.journalPath()); err != nil {
		return err
	}
	if err := fileutil.SyncDir(a.State); err != nil {
		return err
	}
	if !j.RetainWork {
		fileutil.RemoveAll(j.Work)
	}

	return nil
}

func (a *App) recover(ctx context.Context, command string) (bool, error) {
	b, err := os.ReadFile(a.journalPath())
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	var j Journal
	if err := json.Unmarshal(b, &j); err != nil {
		return true, errors.New("журнал операции повреждён; сохранённые файлы оставлены для восстановления")
	}
	if j.Version != 1 || j.Local != a.Local || j.Remote != a.Remote || j.Command != command {
		return true, fmt.Errorf("есть незавершённая операция; повторите исходную команду из %q", a.journalPath())
	}
	if filepath.Dir(j.Work) != a.State || !strings.HasPrefix(filepath.Base(j.Work), "run-") {
		return true, errors.New("недопустимый путь в журнале")
	}
	if command == "push" {
		if !strings.HasPrefix(j.TemporaryRemote, a.Remote+".upload-") || path.Dir(j.TemporaryRemote) != path.Dir(a.Remote) {
			return true, errors.New("недопустимый временный облачный путь в журнале")
		}
		if j.OldRemote != nil && j.Backup != backupPath(a.Remote, j.OldRemote.Created) {
			return true, errors.New("недопустимый путь бэкапа в журнале")
		}
	} else if j.Backup != "" && !j.temporaryPullBackup() && filepath.Dir(j.Backup) != filepath.Join(a.State, "Backups") {
		return true, errors.New("недопустимый локальный бэкап в журнале")
	}
	if err := validateBaselineJournal(&j); err != nil {
		return true, err
	}
	if err := a.validateExcludedBackup(&j); err != nil {
		return true, err
	}
	a.log("Продолжаю незавершённый %s, этап %s.", command, j.Phase)
	if err := a.closed(ctx); err != nil {
		return true, err
	}
	if command == "push" {
		err = a.resumePush(ctx, &j)
		if err == nil {
			a.log("Завершена загрузка ранее подготовленного снимка. Если после сбоя вы редактировали проект, выполните push ещё раз.")
		}
		return true, err
	}

	return true, a.resumePull(ctx, &j)
}
