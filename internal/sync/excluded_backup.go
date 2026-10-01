package sync

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"

	"scrivsync/internal/archive"
	"scrivsync/internal/ctxio"
	"scrivsync/internal/fileutil"
)

func prepareBackupDestination(destination string) error {
	if _, err := os.Lstat(destination); err == nil {
		return fmt.Errorf("имя локального бэкапа уже занято: %s", destination)
	} else if !os.IsNotExist(err) {
		return err
	}
	return os.MkdirAll(filepath.Dir(destination), 0700)
}

func excludedWithParents(files, local archive.Manifest) archive.Manifest {
	result := archive.Manifest{}
	for p, entry := range files {
		result[p] = entry
		for parent := path.Dir(p); parent != "."; parent = path.Dir(parent) {
			result[parent] = local[parent]
		}
	}
	return result
}

// Recovery must use the recorded selection, even if exclusion rules change.
func (a *App) validateExcludedBackup(j *Journal) error {
	if j.ExcludedBackup == "" && len(j.ExcludedFiles) == 0 {
		return nil
	}
	if j.Command != "pull" || !j.temporaryPullBackup() || len(j.ExcludedFiles) == 0 ||
		filepath.Dir(j.ExcludedBackup) != filepath.Join(a.State, "FileBackups") {
		return errors.New("недопустимый бэкап исключённых файлов в журнале")
	}
	files := archive.Manifest{}
	for p, entry := range j.ExcludedFiles {
		if p == "." || !filepath.IsLocal(filepath.FromSlash(p)) || path.Clean(p) != p {
			return errors.New("недопустимый путь исключённого файла в журнале")
		}
		old, ok := j.OldLocal[p]
		if !ok || old != entry {
			return errors.New("снимок исключённых файлов не соответствует локальному проекту")
		}
		if !entry.Dir {
			files[p] = entry
		}
	}
	if len(files) == 0 || !archive.SameManifest(excludedWithParents(files, j.OldLocal), j.ExcludedFiles, true) {
		return errors.New("неполный снимок исключённых файлов в журнале")
	}
	return nil
}

// The original project is already safely renamed to the rollback directory.
// Build the selective copy there before installing the incoming project. A
// partial copy can be rebuilt on recovery; the final backup is never overwritten.
func (a *App) preserveExcludedFiles(ctx context.Context, j *Journal) error {
	if j.ExcludedBackup == "" {
		return nil
	}
	if _, err := os.Lstat(j.ExcludedBackup); err == nil {
		return a.verifyExcludedBackup(ctx, j)
	} else if !os.IsNotExist(err) {
		return err
	}

	staging := filepath.Join(j.Work, "excluded-files")
	if err := os.RemoveAll(staging); err != nil {
		return err
	}
	if err := os.Mkdir(staging, 0700); err != nil {
		return err
	}
	for _, p := range excludedFilePaths(j.ExcludedFiles) {
		from := filepath.Join(j.Backup, filepath.FromSlash(p))
		to := filepath.Join(staging, filepath.FromSlash(p))
		if err := copyExcludedFile(ctx, from, to, j.ExcludedFiles[p]); err != nil {
			return fmt.Errorf("бэкап исключённого файла %q: %w", p, err)
		}
	}
	if err := verifyBackupManifest(ctx, staging, j.ExcludedFiles); err != nil {
		return err
	}
	// Sync all directories, including intermediate parents, before publication.
	for p, entry := range j.ExcludedFiles {
		if entry.Dir {
			if err := fileutil.SyncDir(filepath.Join(staging, filepath.FromSlash(p))); err != nil {
				return err
			}
		}
	}
	if err := fileutil.SyncDir(staging); err != nil {
		return err
	}
	if err := prepareBackupDestination(j.ExcludedBackup); err != nil {
		return err
	}
	if err := os.Rename(staging, j.ExcludedBackup); err != nil {
		return err
	}
	if err := fileutil.SyncDir(j.Work); err != nil {
		return err
	}
	return fileutil.SyncDir(filepath.Dir(j.ExcludedBackup))
}

func copyExcludedFile(ctx context.Context, from, to string, entry archive.Entry) error {
	if err := os.MkdirAll(filepath.Dir(to), 0700); err != nil {
		return err
	}
	source, err := os.Open(from)
	if err != nil {
		return err
	}
	defer fileutil.CloseFileOnReturn(&source)
	destination, err := os.OpenFile(to, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	defer fileutil.CloseFileOnReturn(&destination)
	if _, err := io.Copy(destination, ctxio.Reader{Context: ctx, Source: source}); err != nil {
		return err
	}
	if err := fileutil.CloseFile(&source); err != nil {
		return err
	}
	if err := os.Chtimes(to, entry.Modified, entry.Modified); err != nil {
		return err
	}
	if err := destination.Sync(); err != nil {
		return err
	}
	return fileutil.CloseFile(&destination)
}

func verifyBackupManifest(ctx context.Context, backup string, expected archive.Manifest) error {
	actual, err := archive.Scan(ctx, backup)
	if err != nil {
		return err
	}
	if !archive.SameManifest(actual, expected, true) {
		return fmt.Errorf("бэкап исключённых файлов отсутствует или изменён: %s", backup)
	}
	return nil
}

func (a *App) verifyExcludedBackup(ctx context.Context, j *Journal) error {
	if j.ExcludedBackup == "" {
		return nil
	}
	return verifyBackupManifest(ctx, j.ExcludedBackup, j.ExcludedFiles)
}

func excludedFilePaths(manifest archive.Manifest) []string {
	var paths []string
	for p, entry := range manifest {
		if !entry.Dir {
			paths = append(paths, p)
		}
	}
	sort.Strings(paths)
	return paths
}

func (a *App) logExcludedBackup(j *Journal) {
	if j.ExcludedBackup == "" {
		return
	}
	a.log("Бэкап конфликтующих исключённых файлов: %s", j.ExcludedBackup)
	for _, p := range excludedFilePaths(j.ExcludedFiles) {
		a.log("  %s", p)
	}
}
