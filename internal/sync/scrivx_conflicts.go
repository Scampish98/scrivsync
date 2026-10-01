package sync

import (
	"context"
	"errors"
	"os"
	"path"
	"path/filepath"

	"scrivsync/internal/archive"
	"scrivsync/internal/scrivx"
)

func projectIndex(manifest archive.Manifest) string {
	result := ""
	for p, entry := range manifest {
		if entry.Dir || path.Dir(p) != "." || path.Ext(p) != ".scrivx" {
			continue
		}
		if result != "" {
			return ""
		}
		result = p
	}
	return result
}

func readIndex(ctx context.Context, root, name string, expected archive.Entry) ([]byte, error) {
	data, err := readRegular(ctx, filepath.Join(root, name), scrivx.MaxSize)
	if err != nil {
		return nil, err
	}
	if bytesHash(data) != expected.Hash || int64(len(data)) != expected.Size {
		return nil, errors.New("scrivx изменился после проверки проекта")
	}
	return data, nil
}

func (a *App) scrivxOverrides(ctx context.Context, work string, local, incoming archive.Manifest) (map[string]archive.ConflictOverride, error) {
	name := projectIndex(local)
	if name == "" || projectIndex(incoming) != name || local[name].Hash == incoming[name].Hash {
		return nil, nil
	}
	if local[name].Size > scrivx.MaxSize || incoming[name].Size > scrivx.MaxSize {
		a.log("%s превышает лимит смыслового сравнения; применяется обычная проверка.", name)
		return nil, nil
	}
	left, err := readIndex(ctx, a.Local, name, local[name])
	if err != nil {
		return nil, err
	}
	right, err := readIndex(ctx, filepath.Join(work, "project"), name, incoming[name])
	if err != nil {
		return nil, err
	}
	l, localErr := scrivx.Parse(left)
	r, remoteErr := scrivx.Parse(right)
	if err := errors.Join(localErr, remoteErr); err != nil {
		a.log("Смысловое сравнение %s недоступно; применяется обычная проверка: %v", name, err)
		return nil, nil
	}
	decision := archive.ConflictOverride{}
	if l.Identifier != r.Identifier || l.Version != r.Version {
		decision.Reason = "разные идентификаторы или форматы проекта scrivx"
	} else if l.Hash == r.Hash {
		decision.Accept = true
		a.log("%s: значимое содержимое совпадает; служебные различия не блокируют pull.", name)
	} else {
		base, err := readBaseline(ctx, filepath.Join(a.State, "baseline"))
		if err != nil {
			if !os.IsNotExist(err) {
				a.log("База scrivx недоступна; применяется обычная проверка: %v", err)
			}
			return nil, nil
		}
		if base.Meta.Remote != a.Remote || base.Meta.ProjectPath != name || base.Document.Identifier != l.Identifier || base.Document.Version != l.Version {
			a.log("Сохранённая база не относится к текущему проекту; применяется обычная проверка %s.", name)
			return nil, nil
		}
		if l.Hash == base.Document.Hash {
			decision.Accept = true
			a.log("%s: локально изменились только служебные поля; принимаю изменения архива относительно базы.", name)
		} else if r.Hash == base.Document.Hash {
			decision.Reason = "локально изменено значимое содержимое scrivx; архив соответствует последней синхронизации"
		} else {
			decision.Reason = "значимое содержимое scrivx изменилось с обеих сторон относительно последней синхронизации"
		}
	}
	return map[string]archive.ConflictOverride{name: decision}, nil
}
