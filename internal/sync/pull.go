package sync

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"scrivsync/internal/archive"
	"scrivsync/internal/fileutil"
	"scrivsync/internal/yandex"
)

func scanOptional(ctx context.Context, p string) (archive.Manifest, bool, error) {
	_, err := os.Lstat(p)
	if os.IsNotExist(err) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}

	m, err := archive.Scan(ctx, p)
	return m, true, err
}

func (a *App) pull(ctx context.Context) error {
	before, exists, err := scanOptional(ctx, a.Local)
	if err != nil {
		return err
	}

	remote, err := a.Disk.Stat(ctx, a.Remote)
	if err != nil {
		return err
	}
	if remote == nil {
		return fmt.Errorf("облачный архив не найден: %s", a.Remote)
	}
	if remote.Type != "file" {
		return errors.New("облачный путь указывает на папку")
	}

	work, err := os.MkdirTemp(a.State, "run-")
	if err != nil {
		return err
	}

	keep := false
	defer func() {
		if !keep {
			fileutil.RemoveAll(work)
		}
	}()
	incoming, err := a.downloadProject(ctx, work, remote)
	if err != nil {
		return err
	}

	if err := a.checkPullUnchanged(ctx, remote, before, exists); err != nil {
		return err
	}
	if err := archive.CheckConflicts(before, incoming); err != nil {
		// Retain both the archive and extracted project even if reporting fails.
		keep = true
		reportErr := a.writeConflictReport(ctx, work, before, incoming, err)
		if !a.ForcePull || reportErr != nil {
			return errors.Join(err, reportErr)
		}
		a.log("Обнаружены конфликты; --force принимает архив после сохранения полного локального бэкапа.")
	}

	if exists && archive.SameManifest(before, incoming, false) {
		a.log("Содержимое проекта совпадает с архивом; замена не требуется.")
		return nil
	}

	j, err := a.preparePull(work, before, incoming, exists, keep)
	if err != nil {
		return err
	}

	keep = true
	if err := a.save(j); err != nil {
		return err
	}

	return a.resumePull(ctx, j)
}

func (a *App) resumePull(ctx context.Context, j *Journal) error {
	if j.Phase != "install" {
		return errors.New("неизвестный этап pull в журнале")
	}
	if err := a.closed(ctx); err != nil {
		return err
	}

	stage := filepath.Join(j.Work, "project")
	prepared, stageExists, err := scanOptional(ctx, stage)
	if err != nil {
		return err
	}

	local, localExists, err := scanOptional(ctx, a.Local)
	if err != nil {
		return err
	}
	if !stageExists {
		if !localExists || !archive.SameManifest(local, j.NewLocal, true) {
			return errors.New("невозможно подтвердить завершённую установку; проверьте проект и сохранённый бэкап")
		}
		if j.Backup != "" {
			old, ok, err := scanOptional(ctx, j.Backup)
			if err != nil {
				return err
			}
			if !ok || !archive.SameManifest(old, j.OldLocal, true) {
				return errors.New("локальный бэкап отсутствует или изменён")
			}
		}
	} else {
		if !archive.SameManifest(prepared, j.NewLocal, true) {
			return errors.New("временная распакованная копия изменилась; установка остановлена")
		}
		if err := a.backupLocalProject(ctx, j, local, localExists); err != nil {
			return err
		}

		if err := os.Rename(stage, a.Local); err != nil {
			return fmt.Errorf("установка не завершена; предыдущий проект сохранён в %q; повторите pull: %w", j.Backup, err)
		}
		if err := fileutil.SyncDir(filepath.Dir(stage)); err != nil {
			return err
		}
		if err := fileutil.SyncDir(filepath.Dir(a.Local)); err != nil {
			return err
		}
	}
	if err := a.finish(j); err != nil {
		return err
	}
	a.log("Pull завершён: %s", a.Local)
	if j.RetainWork {
		a.log("Отчёт о принятых конфликтах: %s", filepath.Join(j.Work, "report", "index.txt"))
	}
	if j.Backup != "" && !j.temporaryPullBackup() {
		a.log("Локальный бэкап: %s", j.Backup)
	}

	return nil
}

func (a *App) downloadProject(ctx context.Context, work string, remote *yandex.Resource) (archive.Manifest, error) {
	archivePath := filepath.Join(work, "project.zip")
	a.log("Скачиваю %s.", a.Remote)
	if err := a.Disk.Download(ctx, a.Remote, archivePath); err != nil {
		return nil, err
	}

	hash, size, err := archive.FileHash(ctx, archivePath)
	if err != nil {
		return nil, err
	}
	if !yandex.Matches(remote, hash, size) {
		return nil, errors.New("скачанный архив не соответствует контрольной сумме/размеру в облаке")
	}

	incoming, err := archive.Extract(ctx, archivePath, filepath.Join(work, "project"))
	if err != nil {
		return nil, fmt.Errorf("распаковка: %w", err)
	}

	return incoming, nil
}

func (a *App) checkPullUnchanged(ctx context.Context, remote *yandex.Resource, before archive.Manifest, exists bool) error {
	currentRemote, err := a.Disk.Stat(ctx, a.Remote)
	if err != nil {
		return err
	}
	if !yandex.SameResource(remote, currentRemote) {
		return errors.New("облачный архив изменился во время скачивания; повторите pull")
	}
	if err := a.closed(ctx); err != nil {
		return err
	}

	current, stillExists, err := scanOptional(ctx, a.Local)
	if err != nil {
		return err
	}
	if exists != stillExists || !archive.SameManifest(before, current, true) {
		return errors.New("локальный проект изменился во время скачивания; pull отменён")
	}

	return nil
}

func (a *App) preparePull(work string, before, incoming archive.Manifest, exists, conflicts bool) (*Journal, error) {
	j := &Journal{
		Version:    1,
		Command:    "pull",
		Phase:      "install",
		Local:      a.Local,
		Remote:     a.Remote,
		Work:       work,
		OldLocal:   before,
		NewLocal:   incoming,
		RetainWork: conflicts,
	}
	if exists {
		j.Backup = filepath.Join(work, "previous-project")
		if conflicts {
			j.Backup = filepath.Join(a.State, "Backups", filepath.Base(a.Local)+"_"+a.Now().UTC().Format("2006-01-02T15-04-05Z"))
		}
		if _, err := os.Lstat(j.Backup); err == nil {
			return nil, fmt.Errorf("имя локального бэкапа уже занято: %s", j.Backup)
		} else if !os.IsNotExist(err) {
			return nil, err
		}
		if err := os.MkdirAll(filepath.Dir(j.Backup), 0700); err != nil {
			return nil, err
		}
	}

	return j, nil
}

func (a *App) backupLocalProject(ctx context.Context, j *Journal, local archive.Manifest, localExists bool) error {
	if j.Backup != "" {
		backup, backupExists, err := scanOptional(ctx, j.Backup)
		if err != nil {
			return err
		}
		if backupExists {
			if localExists || !archive.SameManifest(backup, j.OldLocal, true) {
				return errors.New("имя локального бэкапа занято или состояние проекта изменилось; установка остановлена")
			}
		} else {
			if !localExists || !archive.SameManifest(local, j.OldLocal, true) {
				return errors.New("локальный проект изменился после проверки; установка остановлена")
			}
			if err := os.Rename(a.Local, j.Backup); err != nil {
				return err
			}
			if err := fileutil.SyncDir(filepath.Dir(a.Local)); err != nil {
				return err
			}
			if err := fileutil.SyncDir(filepath.Dir(j.Backup)); err != nil {
				return err
			}
		}
	} else if localExists {
		return errors.New("локальная папка появилась после начала pull; установка остановлена")
	}

	return nil
}
