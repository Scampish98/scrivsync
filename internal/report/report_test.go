package report

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"scrivsync/internal/archive"
)

func writeFixture(t *testing.T, root, name string, data []byte) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
}

func reportOptions(t *testing.T, local, incoming string) Options {
	t.Helper()
	left, err := archive.Scan(context.Background(), local)
	if err != nil {
		t.Fatal(err)
	}
	right, err := archive.Scan(context.Background(), incoming)
	if err != nil {
		t.Fatal(err)
	}
	return Options{
		LocalDir: local, IncomingDir: incoming, OutputDir: filepath.Join(t.TempDir(), "report"),
		Local: left, Incoming: right, Conflict: errors.New("test conflict"),
	}
}

func readLog(t *testing.T, options Options, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(options.OutputDir, name))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestReportIncludesEveryContentDifference(t *testing.T) {
	local, incoming := t.TempDir(), t.TempDir()
	writeFixture(t, local, "a.txt", []byte("общая\nлокальная\n"))
	writeFixture(t, incoming, "a.txt", []byte("общая\nскачанная\n"))
	writeFixture(t, local, "b.txt", []byte("только локально\n"))
	writeFixture(t, incoming, "c.txt", []byte("только в архиве\n"))
	writeFixture(t, local, "d.bin", []byte{0, 1})
	writeFixture(t, incoming, "d.bin", []byte{0, 2})
	writeFixture(t, local, "e.rtf", []byte(`{\rtf1 old}`))
	writeFixture(t, incoming, "e.rtf", []byte(`{\rtf1 new}`))
	writeFixture(t, local, "same.txt", []byte("same"))
	writeFixture(t, incoming, "same.txt", []byte("same"))
	if err := os.Chtimes(filepath.Join(local, "same.txt"), time.Now(), time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	options := reportOptions(t, local, incoming)
	if err := Write(context.Background(), options); err != nil {
		t.Fatal(err)
	}

	index := readLog(t, options, "index.txt")
	if strings.Contains(index, "same.txt") || !strings.Contains(index, "Отчёт завершён") {
		t.Fatalf("invalid index: %s", index)
	}
	for name, fragments := range map[string][]string{
		"000001.diff.txt": {"-локальная", "+скачанная", "SHA-256:"},
		"000002.diff.txt": {"В архиве: отсутствует", "+++ /dev/null", "-только локально"},
		"000003.diff.txt": {"Локально: отсутствует", "--- /dev/null", "+только в архиве"},
		"000004.diff.txt": {"Двоичный файл", "Размер: 2 байт", options.Local["d.bin"].Hash, options.Incoming["d.bin"].Hash},
		"000005.diff.txt": {"RTF: сравнивается исходный код", `-{\rtf1 old}`, `+{\rtf1 new}`},
	} {
		if !strings.Contains(index, name) {
			t.Errorf("index missing %s", name)
		}
		log := readLog(t, options, name)
		for _, fragment := range fragments {
			if !strings.Contains(log, fragment) {
				t.Errorf("%s missing %q: %s", name, fragment, log)
			}
		}
	}
}

func TestReportHandlesTypeChangeAndLargeFiles(t *testing.T) {
	local, incoming := t.TempDir(), t.TempDir()
	writeFixture(t, local, "a", []byte("local file\n"))
	writeFixture(t, incoming, "a/child.txt", []byte("incoming child\n"))
	writeFixture(t, local, "large.txt", []byte(strings.Repeat("x", maxTextBytes+1)))
	writeFixture(t, incoming, "large.txt", []byte("new"))
	writeFixture(t, local, "lines.txt", []byte(strings.Repeat("\n", maxTextLines)))
	writeFixture(t, incoming, "lines.txt", []byte("new"))
	options := reportOptions(t, local, incoming)
	if err := Write(context.Background(), options); err != nil {
		t.Fatal(err)
	}
	for name, fragments := range map[string][]string{
		"000001.diff.txt": {"В архиве: папка", "Различие структуры", "-local file"},
		"000002.diff.txt": {"+incoming child"},
		"000003.diff.txt": {"превышает 2 МиБ"},
		"000004.diff.txt": {"50000 переносов строк"},
	} {
		for _, fragment := range fragments {
			if log := readLog(t, options, name); !strings.Contains(log, fragment) {
				t.Errorf("%s missing %q: %s", name, fragment, log)
			}
		}
	}
}

func TestReportDetectsFileChangeAndContinuesOtherComparisons(t *testing.T) {
	local, incoming := t.TempDir(), t.TempDir()
	writeFixture(t, local, "a.txt", []byte("before"))
	writeFixture(t, incoming, "a.txt", []byte("incoming"))
	writeFixture(t, local, "b.txt", []byte("keep"))
	options := reportOptions(t, local, incoming)
	writeFixture(t, local, "a.txt", []byte("changed after scan"))

	if err := Write(context.Background(), options); err == nil {
		t.Fatal("changed file accepted")
	}
	if index := readLog(t, options, "index.txt"); !strings.Contains(index, "ОТЧЁТ НЕ ЗАВЕРШЁН") {
		t.Fatalf("report failure not recorded: %s", index)
	}
	if log := readLog(t, options, "000001.diff.txt"); !strings.Contains(log, "файл изменился после проверки") || strings.Contains(log, "changed after scan") {
		t.Fatalf("misleading diff for changed file: %s", log)
	}
	if log := readLog(t, options, "000002.diff.txt"); !strings.Contains(log, "-keep") {
		t.Fatalf("unaffected file not compared: %s", log)
	}
}

func TestReportDoesNotFollowOutsideSymlink(t *testing.T) {
	local, incoming := t.TempDir(), t.TempDir()
	writeFixture(t, local, "a.txt", []byte("before"))
	options := reportOptions(t, local, incoming)
	outside := filepath.Join(t.TempDir(), "outside.txt")
	if err := os.WriteFile(outside, []byte("private outside data"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(local, "a.txt")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(local, "a.txt")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if err := Write(context.Background(), options); err == nil {
		t.Fatal("symlink accepted")
	}
	if log := readLog(t, options, "000001.diff.txt"); strings.Contains(log, "private outside data") {
		t.Fatal("outside contents leaked")
	}
}
