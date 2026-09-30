// Package report writes diagnostic comparisons without modifying either project.
package report

import (
	"bytes"
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
	"time"
	"unicode/utf8"

	"scrivsync/internal/archive"
	"scrivsync/internal/ctxio"
	"scrivsync/internal/fileutil"
)

// Limits apply per file and prevent a diagnostic report from exhausting memory.
const (
	maxTextBytes = 2 << 20
	maxTextLines = 50_000
)

type Options struct {
	RemotePath  string
	LocalDir    string
	IncomingDir string
	OutputDir   string
	Local       archive.Manifest
	Incoming    archive.Manifest
	Conflict    error
}

// Write produces an index and one log for every differing path. Identical bytes
// with different timestamps are omitted. Errors leave partial reports in place.
func Write(ctx context.Context, options Options) error {
	if err := os.Mkdir(options.OutputDir, 0700); err != nil {
		return err
	}
	local, err := os.OpenRoot(options.LocalDir)
	if err != nil {
		return err
	}
	defer fileutil.Close(local, "локальная папка отчёта")

	incoming, err := os.OpenRoot(options.IncomingDir)
	if err != nil {
		return err
	}
	defer fileutil.Close(incoming, "папка скачанного проекта")

	var index strings.Builder
	fmt.Fprintf(&index, "Сравнение локального проекта и скачанного архива\n\nАрхив на Яндекс Диске: %s\nЛокальный проект: %s\nСкачанный проект: %s\n\n%v\n\n", options.RemotePath, options.LocalDir, options.IncomingDir, options.Conflict)
	index.WriteString("Ниже все различия, включая файлы, которые сами по себе не блокируют pull.\nЗнак -: локальная версия; знак +: скачанная версия.\nДифф показывает содержимое файлов, а не смысловые изменения документов Scrivener.\n\n")

	var failures []error
	for i, name := range differingPaths(options.Local, options.Incoming) {
		if err := ctx.Err(); err != nil {
			failures = append(failures, err)
			break
		}
		logName := fmt.Sprintf("%06d.diff.txt", i+1)
		text, compareErr := compareFile(ctx, local, incoming, name, options.Local, options.Incoming)
		if compareErr != nil {
			text += fmt.Sprintf("\nОШИБКА: %v\nДифф этого файла не завершён.\n", compareErr)
			failures = append(failures, fmt.Errorf("сравнение %q: %w", name, compareErr))
		}
		writeErr := writeFile(filepath.Join(options.OutputDir, logName), text)
		if writeErr != nil {
			failures = append(failures, fmt.Errorf("запись %q: %w", logName, writeErr))
		}

		fmt.Fprintf(&index, "%s — %s\n", logName, name)
		if err := errors.Join(compareErr, writeErr); err != nil {
			fmt.Fprintf(&index, "  ОШИБКА: %v\n", err)
		}
	}
	if len(failures) > 0 {
		index.WriteString("\nОТЧЁТ НЕ ЗАВЕРШЁН: часть сравнений не удалось выполнить.\n")
	} else {
		index.WriteString("\nОтчёт завершён.\n")
	}
	if err := writeFile(filepath.Join(options.OutputDir, "index.txt"), index.String()); err != nil {
		failures = append(failures, err)
	}
	return errors.Join(failures...)
}

func differingPaths(local, incoming archive.Manifest) []string {
	seen := make(map[string]bool, len(local)+len(incoming))
	for name := range local {
		seen[name] = true
	}
	for name := range incoming {
		seen[name] = true
	}

	var paths []string
	for name := range seen {
		left, lok := local[name]
		right, rok := incoming[name]
		if lok && rok && left.Dir == right.Dir && (left.Dir || (left.Hash == right.Hash && left.Size == right.Size)) {
			continue
		}
		paths = append(paths, name)
	}
	sort.Strings(paths)
	return paths
}

func compareFile(ctx context.Context, localRoot, incomingRoot *os.Root, name string, local, incoming archive.Manifest) (string, error) {
	left, lok := local[name]
	right, rok := incoming[name]
	var out strings.Builder
	fmt.Fprintf(&out, "Путь: %s\n\n", name)
	describe(&out, "Локально", left, lok)
	describe(&out, "В архиве", right, rok)
	out.WriteByte('\n')

	if (lok && left.Dir) || (rok && right.Dir) {
		out.WriteString("Различие структуры: наличие папки или смена типа файл/папка. Файлы внутри папок перечислены отдельно.\n")
		if (!lok || left.Dir) && (!rok || right.Dir) {
			return out.String(), nil
		}
	}
	if left.Size > maxTextBytes || right.Size > maxTextBytes {
		out.WriteString("Построчный дифф пропущен: размер одной из версий превышает 2 МиБ. Сравните оригиналы по указанному пути.\n")
		return out.String(), nil
	}

	before, err := readVersion(ctx, localRoot, name, left, lok && !left.Dir)
	if err != nil {
		return out.String(), err
	}
	after, err := readVersion(ctx, incomingRoot, name, right, rok && !right.Dir)
	if err != nil {
		return out.String(), err
	}
	if !isText(before) || !isText(after) {
		out.WriteString("Двоичный файл или текст не в UTF-8: построчный дифф не построен. Размеры и SHA-256 приведены выше.\n")
		return out.String(), nil
	}
	if bytes.Count(before, []byte{'\n'}) >= maxTextLines || bytes.Count(after, []byte{'\n'}) >= maxTextLines {
		out.WriteString("Построчный дифф пропущен: достигнут лимит 50000 переносов строк. Сравните оригиналы по указанному пути.\n")
		return out.String(), nil
	}
	if strings.EqualFold(filepath.Ext(name), ".rtf") {
		out.WriteString("RTF: сравнивается исходный код, включая форматирование и экранированные символы. Это не сравнение только видимого текста.\n\n")
	}
	leftName, rightName := "local/"+name, "archive/"+name
	if !lok || left.Dir {
		leftName = "/dev/null"
	}
	if !rok || right.Dir {
		rightName = "/dev/null"
	}
	diff, err := unifiedDiff(ctx, before, after, leftName, rightName)
	out.WriteString(diff)
	return out.String(), err
}

func describe(out *strings.Builder, label string, entry archive.Entry, exists bool) {
	if !exists {
		fmt.Fprintf(out, "%s: отсутствует\n", label)
		return
	}
	kind := "файл"
	if entry.Dir {
		kind = "папка"
	}
	fmt.Fprintf(out, "%s: %s; изменён %s\n", label, kind, entry.Modified.Format(time.RFC3339Nano))
	if !entry.Dir {
		fmt.Fprintf(out, "  Размер: %d байт; SHA-256: %s\n", entry.Size, entry.Hash)
	}
}

func readVersion(ctx context.Context, root *os.Root, name string, expected archive.Entry, exists bool) ([]byte, error) {
	if !exists {
		return nil, nil
	}
	name = filepath.FromSlash(name)
	info, err := root.Lstat(name)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("файл больше не является обычным файлом")
	}
	f, err := root.Open(name)
	if err != nil {
		return nil, err
	}
	defer fileutil.CloseFileOnReturn(&f)

	data, err := io.ReadAll(ctxio.Reader{Context: ctx, Source: io.LimitReader(f, maxTextBytes+1)})
	if err != nil {
		return nil, err
	}
	hash := sha256.Sum256(data)
	if int64(len(data)) != expected.Size || hex.EncodeToString(hash[:]) != expected.Hash {
		return nil, errors.New("файл изменился после проверки конфликтов; сохранённые метаданные не соответствуют текущему содержимому")
	}
	return data, nil
}

func isText(data []byte) bool {
	return utf8.Valid(data) && !bytes.ContainsRune(data, 0)
}

func writeFile(path, text string) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer fileutil.CloseFileOnReturn(&f)

	if _, err := io.WriteString(f, text); err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		return err
	}
	return fileutil.CloseFile(&f)
}
