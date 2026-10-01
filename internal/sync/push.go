package sync

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"scrivsync/internal/archive"
	"scrivsync/internal/ctxio"
	"scrivsync/internal/fileutil"
	"scrivsync/internal/yandex"
)

func backupPath(remote string, created time.Time) string {
	base := path.Base(remote)
	ext := path.Ext(base)
	return path.Join(path.Dir(remote), "Backups", strings.TrimSuffix(base, ext)+"_"+created.UTC().Format("2006-01-02T15-04-05Z")+ext)
}

func (a *App) push(ctx context.Context) error {
	a.log("Подготавливаю архив проекта %q.", a.Local)
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
	archivePath := filepath.Join(work, "project.zip")
	if _, err := archive.Create(ctx, a.Local, archivePath); err != nil {
		return err
	}
	if err := a.closed(ctx); err != nil {
		return err
	}

	hash, size, err := archive.FileHash(ctx, archivePath)
	if err != nil {
		return err
	}

	j, err := a.preparePush(ctx, work, hash, size)
	if err != nil {
		return err
	}

	if err := a.prepareBaseline(ctx, j); err != nil {
		return err
	}

	// Retain the snapshot even if saving the journal has an uncertain outcome.
	keep = true
	if err := a.save(j); err != nil {
		return err
	}

	return a.resumePush(ctx, j)
}

func (a *App) verifyRemote(ctx context.Context, p, hash string, size int64) error {
	// Upload acceptance may precede visibility in resource metadata.
	for attempt := 0; attempt < 30; attempt++ {
		r, err := a.Disk.Stat(ctx, p)
		if err != nil {
			return err
		}
		if r != nil {
			if yandex.Matches(r, hash, size) {
				return nil
			}
			return fmt.Errorf("контрольная сумма или размер облачного файла не совпали: %s", p)
		}
		if err := ctxio.Wait(ctx, time.Second); err != nil {
			return err
		}
	}

	return fmt.Errorf("загруженный файл пока не доступен: %s; повторите команду позже", p)
}

func (a *App) resumePush(ctx context.Context, j *Journal) error {
	if j.Phase == "upload" {
		if err := a.uploadSnapshot(ctx, j); err != nil {
			return err
		}
	}

	if j.Phase == "rotate" {
		if err := a.rotateBackup(ctx, j); err != nil {
			return err
		}
	}

	if j.Phase != "publish" {
		return errors.New("неизвестный этап push в журнале")
	}

	return a.publishSnapshot(ctx, j)
}

func (a *App) uploadSnapshot(ctx context.Context, j *Journal) error {
	hash, size, err := archive.FileHash(ctx, filepath.Join(j.Work, "project.zip"))
	if err != nil {
		return err
	}
	if hash != j.ArchiveHash || size != j.ArchiveSize {
		return errors.New("сохранённый временный архив повреждён")
	}

	current, err := a.Disk.Stat(ctx, a.Remote)
	if err != nil {
		return err
	}
	if !yandex.SameResource(current, j.OldRemote) {
		return errors.New("облачный архив изменился после начала push; операция остановлена")
	}

	tmp, err := a.Disk.Stat(ctx, j.TemporaryRemote)
	if err != nil {
		return err
	}
	if tmp == nil {
		a.log("Загружаю новый архив во временный облачный файл.")
		if err := a.Disk.Upload(ctx, filepath.Join(j.Work, "project.zip"), j.TemporaryRemote); err != nil {
			return err
		}
	}
	if err := a.verifyRemote(ctx, j.TemporaryRemote, j.ArchiveHash, j.ArchiveSize); err != nil {
		return err
	}
	j.Phase = "rotate"
	if err := a.save(j); err != nil {
		return err
	}

	return nil
}

func (a *App) rotateBackup(ctx context.Context, j *Journal) error {
	if err := a.closed(ctx); err != nil {
		return err
	}
	if err := a.verifyRemote(ctx, j.TemporaryRemote, j.ArchiveHash, j.ArchiveSize); err != nil {
		return err
	}

	current, err := a.Disk.Stat(ctx, a.Remote)
	if err != nil {
		return err
	}
	if j.OldRemote != nil {
		backup, err := a.Disk.Stat(ctx, j.Backup)
		if err != nil {
			return err
		}
		if current != nil {
			if !yandex.SameResource(current, j.OldRemote) {
				return errors.New("текущий облачный архив изменился; push остановлен")
			}
			if backup != nil {
				return fmt.Errorf("имя бэкапа уже занято: %s; перезапись запрещена", j.Backup)
			}
			a.log("Сохраняю предыдущий архив: %s", j.Backup)
			if err := a.Disk.Move(ctx, a.Remote, j.Backup); err != nil {
				return err
			}
			backup, err = a.Disk.Stat(ctx, j.Backup)
			if err != nil {
				return err
			}
		}
		if !yandex.Matches(backup, j.OldRemote.SHA256, j.OldRemote.Size) {
			return errors.New("не удалось подтвердить сохранность облачного бэкапа")
		}
	} else if current != nil {
		return errors.New("по целевому пути появился другой архив; push остановлен")
	}
	j.Phase = "publish"
	if err := a.save(j); err != nil {
		return err
	}

	return nil
}

func (a *App) publishSnapshot(ctx context.Context, j *Journal) error {
	current, err := a.Disk.Stat(ctx, a.Remote)
	if err != nil {
		return err
	}

	tmp, err := a.Disk.Stat(ctx, j.TemporaryRemote)
	if err != nil {
		return err
	}
	if current == nil {
		if !yandex.Matches(tmp, j.ArchiveHash, j.ArchiveSize) {
			return errors.New("временный облачный архив отсутствует или изменён; бэкап сохранён")
		}
		if err := a.Disk.Move(ctx, j.TemporaryRemote, a.Remote); err != nil {
			return err
		}
	} else if tmp != nil || !yandex.Matches(current, j.ArchiveHash, j.ArchiveSize) {
		return errors.New("целевой облачный путь занят неожиданным файлом; перезапись запрещена")
	}
	if err := a.verifyRemote(ctx, a.Remote, j.ArchiveHash, j.ArchiveSize); err != nil {
		return err
	}
	if err := a.installBaseline(ctx, j); err != nil {
		return fmt.Errorf("сохранение базы scrivx не завершено; повторите push: %w", err)
	}
	if err := a.finish(j); err != nil {
		return err
	}
	a.log("Push завершён: %s", a.Remote)
	if j.Backup != "" {
		a.log("Бэкап: %s", j.Backup)
	}

	return nil
}

func (a *App) preparePush(ctx context.Context, work, hash string, size int64) (*Journal, error) {
	old, err := a.Disk.Stat(ctx, a.Remote)
	if err != nil {
		return nil, err
	}
	if old != nil && (old.Type != "file" || old.Created.IsZero()) {
		return nil, errors.New("текущий облачный архив не является файлом или не имеет даты создания")
	}

	j := &Journal{
		Version:         1,
		Command:         "push",
		Phase:           "upload",
		Local:           a.Local,
		Remote:          a.Remote,
		Work:            work,
		OldRemote:       old,
		ArchiveHash:     hash,
		ArchiveSize:     size,
		TemporaryRemote: a.Remote + ".upload-" + strings.TrimPrefix(filepath.Base(work), "run-"),
	}
	if old != nil {
		j.Backup = backupPath(a.Remote, old.Created)
		r, err := a.Disk.Stat(ctx, j.Backup)
		if err != nil {
			return nil, err
		}
		if r != nil {
			return nil, fmt.Errorf("имя бэкапа уже занято: %s; push отменён", j.Backup)
		}
	}
	if err := a.Disk.Mkdir(ctx, path.Dir(a.Remote)); err != nil {
		return nil, err
	}
	if old != nil {
		if err := a.Disk.Mkdir(ctx, path.Dir(j.Backup)); err != nil {
			return nil, err
		}
	}

	return j, nil
}
