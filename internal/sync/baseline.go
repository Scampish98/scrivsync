package sync

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"scrivsync/internal/archive"
	"scrivsync/internal/ctxio"
	"scrivsync/internal/fileutil"
	"scrivsync/internal/scrivx"
)

type baselineMeta struct {
	Version        int       `json:"version"`
	ProjectPath    string    `json:"project_path"`
	Identifier     string    `json:"project_identifier"`
	ProjectVersion string    `json:"project_version"`
	Remote         string    `json:"remote_path"`
	SHA256         string    `json:"sha256"`
	SavedAt        time.Time `json:"saved_at"`
}

type baseline struct {
	Meta     baselineMeta
	Document scrivx.Document
}

func bytesHash(data []byte) string {
	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:])
}

func (a *App) prepareBaseline(ctx context.Context, j *Journal) error {
	name, data, err := archive.ReadProjectIndex(ctx, filepath.Join(j.Work, "project.zip"))
	if err != nil {
		return fmt.Errorf("подготовка базовой версии scrivx: %w", err)
	}
	j.BaselineUpdate = true
	if name == "" {
		return nil
	}
	document, err := scrivx.Parse(data)
	if err != nil {
		a.log("Базовая версия %s не создана; применяется обычная проверка: %v", name, err)
		return nil
	}
	if j.Command == "pull" {
		entry, ok := j.NewLocal[name]
		if !ok || entry.Dir || entry.Hash != bytesHash(data) || entry.Size != int64(len(data)) {
			return errors.New("базовая версия scrivx не соответствует устанавливаемому проекту")
		}
	}
	meta := baselineMeta{Version: 1, ProjectPath: name, Identifier: document.Identifier, ProjectVersion: document.Version, Remote: a.Remote, SHA256: bytesHash(data), SavedAt: a.Now().UTC()}
	encoded, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return err
	}
	folder := filepath.Join(j.Work, "baseline")
	if err := os.Mkdir(folder, 0700); err != nil {
		return err
	}
	if err := writeSynced(filepath.Join(folder, "project.scrivx"), data); err != nil {
		return err
	}
	if err := writeSynced(filepath.Join(folder, "meta.json"), append(encoded, '\n')); err != nil {
		return err
	}
	if err := fileutil.SyncDir(folder); err != nil {
		return err
	}
	if err := fileutil.SyncDir(j.Work); err != nil {
		return err
	}
	j.Baseline = &meta
	return nil
}

func writeSynced(filename string, data []byte) error {
	f, err := os.OpenFile(filename, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer fileutil.CloseFileOnReturn(&f)
	if _, err := f.Write(data); err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		return err
	}
	return fileutil.CloseFile(&f)
}

func readRegular(ctx context.Context, filename string, limit int64) ([]byte, error) {
	before, err := os.Lstat(filename)
	if err != nil {
		return nil, err
	}
	if !before.Mode().IsRegular() || before.Size() > limit {
		return nil, errors.New("файл отсутствует, слишком велик или не является обычным файлом")
	}
	f, err := os.Open(filename)
	if err != nil {
		return nil, err
	}
	defer fileutil.CloseFileOnReturn(&f)
	data, err := io.ReadAll(ctxio.Reader{Context: ctx, Source: io.LimitReader(f, limit+1)})
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, errors.New("файл превышает допустимый размер")
	}
	if err := fileutil.CloseFile(&f); err != nil {
		return nil, err
	}
	return data, nil
}

func readBaseline(ctx context.Context, folder string) (*baseline, error) {
	info, err := os.Lstat(folder)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("базовая версия должна быть обычной папкой")
	}
	encoded, err := readRegular(ctx, filepath.Join(folder, "meta.json"), 65536)
	if err != nil {
		return nil, err
	}
	var meta baselineMeta
	if err := json.Unmarshal(encoded, &meta); err != nil {
		return nil, err
	}
	if err := validateBaselineMeta(&meta); err != nil {
		return nil, err
	}
	data, err := readRegular(ctx, filepath.Join(folder, "project.scrivx"), scrivx.MaxSize)
	if err != nil {
		return nil, err
	}
	if bytesHash(data) != meta.SHA256 {
		return nil, errors.New("контрольная сумма базовой версии scrivx не совпала")
	}
	document, err := scrivx.Parse(data)
	if err != nil {
		return nil, err
	}
	if document.Identifier != meta.Identifier || document.Version != meta.ProjectVersion {
		return nil, errors.New("метаданные базовой версии scrivx не совпали")
	}
	return &baseline{Meta: meta, Document: document}, nil
}

func validateBaselineMeta(meta *baselineMeta) error {
	if meta.Version != 1 || meta.Identifier == "" || meta.ProjectVersion != "2.0" || meta.Remote == "" ||
		path.Base(meta.ProjectPath) != meta.ProjectPath || !filepath.IsLocal(meta.ProjectPath) ||
		strings.ContainsAny(meta.ProjectPath, "\\:") || path.Ext(meta.ProjectPath) != ".scrivx" {
		return errors.New("неподдерживаемые метаданные базовой версии scrivx")
	}
	hash, err := hex.DecodeString(meta.SHA256)
	if err != nil || len(hash) != sha256.Size {
		return errors.New("недопустимая контрольная сумма базовой версии")
	}
	return nil
}

func validateBaselineJournal(j *Journal) error {
	if j.Baseline != nil {
		if !j.BaselineUpdate || j.Baseline.Remote != j.Remote {
			return errors.New("недопустимая базовая версия в журнале")
		}
		if j.Command == "pull" {
			entry, ok := j.NewLocal[j.Baseline.ProjectPath]
			if !ok || entry.Dir || entry.Hash != j.Baseline.SHA256 {
				return errors.New("базовая версия в журнале не соответствует принятому проекту")
			}
		}
		return validateBaselineMeta(j.Baseline)
	}
	return nil
}

// Rotate the old directory into the journal-owned work directory, then install
// the prepared pair of files. Every interruption leaves either the old pair,
// the new pair, or both recoverable pairs. No partially updated pair is exposed.
func (a *App) installBaseline(ctx context.Context, j *Journal) error {
	if !j.BaselineUpdate {
		return nil
	}
	current := filepath.Join(a.State, "baseline")
	previous := filepath.Join(j.Work, "previous-baseline")
	prepared := filepath.Join(j.Work, "baseline")
	_, currentErr := os.Lstat(current)
	if currentErr != nil && !os.IsNotExist(currentErr) {
		return currentErr
	}
	if currentErr == nil && j.Baseline != nil {
		existing, err := readBaseline(ctx, current)
		if err == nil && existing.Meta == *j.Baseline {
			return fileutil.SyncDir(a.State)
		}
	}
	if j.Baseline != nil {
		snapshot, err := readBaseline(ctx, prepared)
		if err != nil {
			return fmt.Errorf("подготовленная базовая версия недоступна: %w", err)
		}
		if snapshot.Meta != *j.Baseline {
			return errors.New("подготовленная базовая версия изменилась")
		}
	}
	_, previousErr := os.Lstat(previous)
	if previousErr != nil && !os.IsNotExist(previousErr) {
		return previousErr
	}
	if currentErr == nil {
		if previousErr == nil {
			return errors.New("неожиданная базовая версия после прерывания; сохранённые данные оставлены")
		}
		if err := os.Rename(current, previous); err != nil {
			return err
		}
		if err := fileutil.SyncDir(a.State); err != nil {
			return err
		}
		if err := fileutil.SyncDir(j.Work); err != nil {
			return err
		}
	}
	if j.Baseline != nil {
		if err := os.Rename(prepared, current); err != nil {
			return err
		}
		if err := fileutil.SyncDir(j.Work); err != nil {
			return err
		}
	}
	return fileutil.SyncDir(a.State)
}
