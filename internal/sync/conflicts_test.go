package sync

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"scrivsync/internal/archive"
)

func TestPullConflictPreservesDownloadAndReportsAcrossRetries(t *testing.T) {
	d := newFake()
	a := testApp(t, d)
	writeTestFile(t, a.Local, "Files/chapter.rtf", "local\n", testTime.Add(time.Hour))
	download := archiveBytes(t, "remote\n", testTime)
	d.put(a.Remote, download)

	for attempt := 0; attempt < 2; attempt++ {
		var conflict *archive.ConflictError
		if err := a.pull(context.Background()); !errors.As(err, &conflict) {
			t.Fatalf("expected conflict, got %v", err)
		}
	}
	assertLocal(t, a, "local\n")
	if _, err := os.Stat(a.journalPath()); !os.IsNotExist(err) {
		t.Fatal("diagnostics created a resumable installation")
	}

	runs, err := filepath.Glob(filepath.Join(a.State, "run-*"))
	if err != nil || len(runs) != 2 {
		t.Fatalf("retained runs = %v, %v", runs, err)
	}
	for _, run := range runs {
		zip, err := os.ReadFile(filepath.Join(run, "project.zip"))
		if err != nil || !bytes.Equal(zip, download) {
			t.Fatalf("archive not preserved: %v", err)
		}
		incoming, err := os.ReadFile(filepath.Join(run, "project", "Files", "chapter.rtf"))
		if err != nil || string(incoming) != "remote\n" {
			t.Fatalf("extracted version not preserved: %v", err)
		}
		indexPath := filepath.Join(run, "report", "index.txt")
		index, err := os.ReadFile(indexPath)
		if err != nil || !strings.Contains(string(index), "Files/chapter.rtf") {
			t.Fatalf("missing index: %v", err)
		}
		log, err := os.ReadFile(filepath.Join(run, "report", "000001.diff.txt"))
		if err != nil || !strings.Contains(string(log), "-local\n+remote\n") {
			t.Fatalf("invalid diff: %s, %v", log, err)
		}
		if !strings.Contains(a.Out.(*bytes.Buffer).String(), indexPath) {
			t.Fatal("report location not printed")
		}
	}
}

func TestPullRetainsConflictDataWhenReportCannotBeWritten(t *testing.T) {
	for _, force := range []bool{false, true} {
		t.Run(fmt.Sprint(force), func(t *testing.T) {
			d := newFake()
			a := testApp(t, d)
			a.ForcePull = force
			writeTestFile(t, a.Local, "Files/chapter.rtf", "local", testTime.Add(time.Hour))
			d.put(a.Remote, archiveBytes(t, "remote", testTime))
			d.afterDownload = func() {
				runs, err := filepath.Glob(filepath.Join(a.State, "run-*"))
				if err != nil || len(runs) != 1 {
					t.Fatalf("unexpected temporary folders: %v, %v", runs, err)
				}
				// A pre-existing path deterministically simulates a report creation error.
				if err := os.WriteFile(filepath.Join(runs[0], "report"), []byte("occupied"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			var conflict *archive.ConflictError
			err := a.pull(context.Background())
			if !errors.As(err, &conflict) || !strings.Contains(err.Error(), "не удалось завершить отчёт") {
				t.Fatalf("expected conflict and report error, got %v", err)
			}
			assertLocal(t, a, "local")
			runs, _ := filepath.Glob(filepath.Join(a.State, "run-*"))
			if len(runs) != 1 {
				t.Fatal("download lost after report failure")
			}
			for _, name := range []string{"project.zip", "project/Files/chapter.rtf"} {
				if _, err := os.Stat(filepath.Join(runs[0], filepath.FromSlash(name))); err != nil {
					t.Fatalf("retained data missing: %s, %v", name, err)
				}
			}
		})
	}
}
