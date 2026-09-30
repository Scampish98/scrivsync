package archive

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"scrivsync/internal/ctxio"
	"scrivsync/internal/fileutil"
)

func Create(ctx context.Context, root, dest string) (Manifest, error) {
	before, err := Scan(ctx, root)
	if err != nil {
		return nil, err
	}
	if len(before) == 0 {
		return nil, errors.New("проект пуст; push отменён")
	}

	base := filepath.Base(root)
	if err := portableName(base); err != nil {
		return nil, err
	}

	if err := writeZIP(ctx, root, dest, before); err != nil {
		return nil, err
	}

	after, err := Scan(ctx, root)
	if err != nil {
		return nil, err
	}
	if !SameManifest(before, after, true) {
		return nil, errors.New("проект изменился во время упаковки; повторите push после закрытия Scrivener")
	}

	return before, nil
}

func writeZIP(ctx context.Context, root, dest string, before Manifest) error {
	f, err := os.OpenFile(dest, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}

	defer fileutil.CloseFileOnReturn(&f)
	zw := zip.NewWriter(f)
	closed := false
	defer func() {
		if !closed {
			fileutil.Close(zw, "запись ZIP-архива")
		}
	}()
	keys := make([]string, 0, len(before))
	for p := range before {
		keys = append(keys, p)
	}
	sort.Strings(keys)
	for _, p := range keys {
		if err := writeEntry(ctx, zw, root, p, before[p]); err != nil {
			return err
		}
	}
	closed = true
	if err := zw.Close(); err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		return err
	}
	if err := fileutil.CloseFile(&f); err != nil {
		return err
	}

	return nil
}

func writeEntry(ctx context.Context, zw *zip.Writer, root, p string, e Entry) error {
	name := filepath.Base(root) + "/" + p
	h := &zip.FileHeader{Name: name, Method: zip.Deflate, Modified: e.Modified.UTC()}
	if e.Dir {
		h.Name += "/"
		h.SetMode(os.ModeDir | 0755)
		h.Method = zip.Store
	} else {
		h.SetMode(0644)
	}

	w, err := zw.CreateHeader(h)
	if err != nil {
		return err
	}
	if e.Dir {
		return nil
	}

	in, err := os.Open(filepath.Join(root, filepath.FromSlash(p)))
	if err != nil {
		return err
	}

	hash := sha256.New()
	n, copyErr := io.Copy(io.MultiWriter(w, hash), ctxio.Reader{Context: ctx, Source: in})
	closeErr := in.Close()
	if err := errors.Join(copyErr, closeErr); err != nil {
		return err
	}
	if n != e.Size || hex.EncodeToString(hash.Sum(nil)) != e.Hash {
		return fmt.Errorf("файл изменился во время упаковки: %q", p)
	}

	return nil
}

// Extract validates all paths before creating the destination directory.
// A single top-level project folder is stripped to allow different local names.
func Extract(ctx context.Context, archive, dest string) (Manifest, error) {
	r, err := zip.OpenReader(archive)
	if err != nil {
		return nil, err
	}

	defer fileutil.Close(r, "ZIP-архив")

	manifest, err := validateZIP(r.File)
	if err != nil {
		return nil, err
	}
	if err := os.Mkdir(dest, 0700); err != nil {
		return nil, err
	}

	for _, f := range r.File {
		if err := extractEntry(ctx, f, dest, manifest); err != nil {
			return nil, err
		}
	}

	if err := restoreDirectoryTimes(dest, manifest); err != nil {
		return nil, err
	}

	return Scan(ctx, dest)
}

func validateZIP(files []*zip.File) (Manifest, error) {
	if len(files) == 0 || len(files) > 100000 {
		return nil, errors.New("архив пуст или содержит более 100000 записей")
	}

	m := Manifest{}
	seen := map[string]bool{}
	root := ""
	var total uint64
	for _, f := range files {
		name := strings.TrimSuffix(f.Name, "/")
		if err := portableName(name); err != nil {
			return nil, err
		}
		if seen[name] {
			return nil, fmt.Errorf("повторяющийся путь в архиве: %q", name)
		}
		seen[name] = true
		parts := strings.SplitN(name, "/", 2)
		if root == "" {
			root = parts[0]
		}
		if root != parts[0] {
			return nil, errors.New("архив должен содержать одну корневую папку проекта")
		}
		if !f.Mode().IsRegular() && !f.Mode().IsDir() {
			return nil, fmt.Errorf("ссылка или специальный файл в ZIP: %q", name)
		}
		if len(parts) == 1 {
			if !f.FileInfo().IsDir() {
				return nil, errors.New("файлы ZIP должны находиться внутри папки проекта")
			}
			continue
		}
		if f.Modified.IsZero() || f.Modified.Year() < 1980 {
			return nil, fmt.Errorf("нет достоверной даты изменения: %q", name)
		}
		total += f.UncompressedSize64
		if f.UncompressedSize64 > 100<<30 || total > 100<<30 {
			return nil, errors.New("распакованный архив превышает лимит 100 ГиБ")
		}
		m[parts[1]] = Entry{Dir: f.FileInfo().IsDir(), Size: int64(f.UncompressedSize64), Modified: f.Modified.UTC()}
	}
	if len(m) == 0 {
		return nil, errors.New("в архиве нет проекта")
	}
	if err := checkNames(m); err != nil {
		return nil, err
	}

	return m, nil
}

func extractEntry(ctx context.Context, f *zip.File, dest string, manifest Manifest) error {
	parts := strings.SplitN(strings.TrimSuffix(f.Name, "/"), "/", 2)
	if len(parts) == 1 {
		return nil
	}

	rel := parts[1]
	e := manifest[rel]
	target := filepath.Join(dest, filepath.FromSlash(rel))
	if e.Dir {
		return os.MkdirAll(target, 0755)
	}
	if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
		return err
	}

	in, err := f.Open()
	if err != nil {
		return err
	}

	out, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
	if err != nil {
		fileutil.Close(in, "файл из ZIP")
		return err
	}

	n, copyErr := io.Copy(out, ctxio.Reader{Context: ctx, Source: io.LimitReader(in, e.Size+1)})
	inErr := in.Close()
	syncErr := out.Sync()
	closeErr := out.Close()
	if err := errors.Join(copyErr, inErr, syncErr, closeErr); err != nil {
		return err
	}
	if n != e.Size {
		return fmt.Errorf("неверный размер файла ZIP: %q", rel)
	}
	if err := os.Chtimes(target, e.Modified, e.Modified); err != nil {
		return err
	}

	return nil
}

func restoreDirectoryTimes(dest string, m Manifest) error {
	// Restore directory timestamps after writing their children; deepest first.
	keys := make([]string, 0, len(m))
	for p := range m {
		keys = append(keys, p)
	}
	sort.Sort(sort.Reverse(sort.StringSlice(keys)))
	for _, p := range keys {
		e := m[p]
		if e.Dir {
			if err := os.Chtimes(filepath.Join(dest, filepath.FromSlash(p)), e.Modified, e.Modified); err != nil {
				return err
			}
		}
	}

	return nil
}
