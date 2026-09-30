package archive

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"scrivsync/internal/ctxio"
	"scrivsync/internal/fileutil"
)

type Entry struct {
	Dir      bool      `json:"directory,omitempty"`
	Size     int64     `json:"size,omitempty"`
	Hash     string    `json:"sha256,omitempty"`
	Modified time.Time `json:"modified"`
}

type Manifest map[string]Entry

func FileHash(ctx context.Context, p string) (string, int64, error) {
	f, err := os.Open(p)
	if err != nil {
		return "", 0, err
	}

	defer fileutil.CloseFileOnReturn(&f)
	h := sha256.New()
	n, err := io.Copy(h, ctxio.Reader{Context: ctx, Source: f})
	return hex.EncodeToString(h.Sum(nil)), n, err
}

func Scan(ctx context.Context, root string) (Manifest, error) {
	fi, err := os.Lstat(root)
	if err != nil {
		return nil, err
	}
	if !fi.IsDir() || fi.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("проект должен быть обычной папкой")
	}

	m := Manifest{}
	err = filepath.WalkDir(root, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if p == root {
			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if err := portableName(rel); err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		e := Entry{Dir: d.IsDir(), Modified: info.ModTime().UTC()}
		if !e.Dir {
			if !info.Mode().IsRegular() {
				return fmt.Errorf("неподдерживаемый объект (например, ссылка): %q", rel)
			}
			e.Hash, e.Size, err = FileHash(ctx, p)
			if err != nil {
				return err
			}
			after, err := os.Lstat(p)
			if err != nil {
				return err
			}
			if !after.Mode().IsRegular() || !os.SameFile(info, after) || info.Size() != e.Size || !info.ModTime().Equal(after.ModTime()) || info.Size() != after.Size() {
				return fmt.Errorf("файл изменился при чтении: %q", rel)
			}
		}
		m[rel] = e
		return nil
	})
	if err != nil {
		return nil, err
	}
	if err := checkNames(m); err != nil {
		return nil, err
	}

	return m, nil
}

func portableName(p string) error {
	if p == "" || strings.HasPrefix(p, "/") || strings.ContainsAny(p, "\\:\x00\r\n<>\"|?*") {
		return fmt.Errorf("недопустимый путь файла: %q", p)
	}

	for _, part := range strings.Split(p, "/") {
		if part == "" || part == "." || part == ".." || strings.HasSuffix(part, ".") || strings.HasSuffix(part, " ") {
			return fmt.Errorf("непереносимое имя файла: %q", p)
		}
		base := strings.ToUpper(strings.SplitN(part, ".", 2)[0])
		if base == "CON" || base == "PRN" || base == "AUX" || base == "NUL" || (len(base) == 4 && (strings.HasPrefix(base, "COM") || strings.HasPrefix(base, "LPT")) && base[3] >= '1' && base[3] <= '9') {
			return fmt.Errorf("имя зарезервировано Windows: %q", p)
		}
	}

	return nil
}

func checkNames(m Manifest) error {
	seen := map[string]string{}
	for p := range m {
		for s := p; s != "."; s = path.Dir(s) {
			key := strings.ToLower(s)
			if prev, ok := seen[key]; ok && prev != s {
				return fmt.Errorf("имена отличаются только регистром: %q и %q", prev, s)
			}
			seen[key] = s
			if s != p {
				if e, ok := m[s]; ok && !e.Dir {
					return fmt.Errorf("файл используется как папка: %q", s)
				}
			}
		}
	}

	return nil
}

func SameManifest(a, b Manifest, times bool) bool {
	if len(a) != len(b) {
		return false
	}

	for p, x := range a {
		y, ok := b[p]
		if !ok || x.Dir != y.Dir || x.Hash != y.Hash || x.Size != y.Size {
			return false
		}
		if times && !x.Dir && !x.Modified.Equal(y.Modified) {
			return false
		}
	}

	return true
}
