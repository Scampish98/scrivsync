package sync

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"scrivsync/internal/archive"
)

func TestSelectiveBackupRecovery(t *testing.T) {
	for phase := 0; phase < 4; phase++ {
		t.Run(fmt.Sprint(phase), func(t *testing.T) {
			ctx := context.Background()
			a := testApp(t, newFake())
			writeTestFile(t, a.Local, "Files/chapter.rtf", "old", testTime.Add(-time.Hour))
			writeTestFile(t, a.Local, "Settings/ui.plist", "local settings", testTime.Add(time.Hour))
			before, err := archive.Scan(ctx, a.Local)
			if err != nil {
				t.Fatal(err)
			}
			work, err := os.MkdirTemp(a.State, "run-")
			if err != nil {
				t.Fatal(err)
			}
			stage := filepath.Join(work, "project")
			writeTestFile(t, stage, "Files/chapter.rtf", "new", testTime)
			incoming, err := archive.Scan(ctx, stage)
			if err != nil {
				t.Fatal(err)
			}
			excluded, err := archive.CompareConflicts(before, incoming)
			if err != nil {
				t.Fatal(err)
			}
			j, err := a.preparePull(work, before, incoming, true, false, excluded)
			if err != nil {
				t.Fatal(err)
			}
			if err := a.save(j); err != nil {
				t.Fatal(err)
			}
			if phase >= 1 {
				if err := os.Rename(a.Local, j.Backup); err != nil {
					t.Fatal(err)
				}
				// A crash can leave a partially written, unverified copy.
				writeTestFile(t, filepath.Join(work, "excluded-files"), "Settings/ui.plist", "partial", testTime)
			}
			if phase >= 2 {
				if err := a.preserveExcludedFiles(ctx, j); err != nil {
					t.Fatal(err)
				}
			}
			if phase >= 3 {
				if err := os.Rename(stage, a.Local); err != nil {
					t.Fatal(err)
				}
			}
			if recovered, err := a.recover(ctx, "pull"); !recovered || err != nil {
				t.Fatalf("recovery: %v, %v", recovered, err)
			}
			assertLocal(t, a, "new")
			if err := verifyBackupManifest(ctx, j.ExcludedBackup, j.ExcludedFiles); err != nil {
				t.Fatal(err)
			}
			content, err := os.ReadFile(filepath.Join(j.ExcludedBackup, "Settings", "ui.plist"))
			if err != nil || string(content) != "local settings" {
				t.Fatal("local settings lost")
			}
			if _, err := os.Stat(work); !os.IsNotExist(err) {
				t.Fatal("rollback data not cleaned")
			}
			if _, err := os.Stat(a.journalPath()); !os.IsNotExist(err) {
				t.Fatal("journal not cleaned")
			}
		})
	}
}

func TestSelectiveBackupCollisionLeavesLocalUntouched(t *testing.T) {
	d := newFake()
	a := testApp(t, d)
	writeTestFile(t, a.Local, "Files/chapter.rtf", "old", testTime.Add(-time.Hour))
	writeTestFile(t, a.Local, "Settings/ui.plist", "local settings", testTime)
	d.put(a.Remote, archiveBytes(t, "new", testTime))
	backup := filepath.Join(a.State, "FileBackups", filepath.Base(a.Local)+"_"+testTime.Format("2006-01-02T15-04-05Z"))
	writeTestFile(t, backup, "keep.txt", "previous backup", testTime)
	if err := a.Run(context.Background(), "pull"); err == nil || !strings.Contains(err.Error(), "имя локального бэкапа уже занято") {
		t.Fatalf("unexpected result: %v", err)
	}
	assertLocal(t, a, "old")
	content, err := os.ReadFile(filepath.Join(backup, "keep.txt"))
	if err != nil || string(content) != "previous backup" {
		t.Fatal("existing backup overwritten")
	}
	if _, err := os.Stat(a.journalPath()); !os.IsNotExist(err) {
		t.Fatal("collision created installation journal")
	}
}

func TestSelectiveBackupFailureStopsInstallation(t *testing.T) {
	ctx := context.Background()
	a := testApp(t, newFake())
	writeTestFile(t, a.Local, "Files/chapter.rtf", "old", testTime.Add(-time.Hour))
	writeTestFile(t, a.Local, "Settings/ui.plist", "local settings", testTime)
	before, err := archive.Scan(ctx, a.Local)
	if err != nil {
		t.Fatal(err)
	}
	work, err := os.MkdirTemp(a.State, "run-")
	if err != nil {
		t.Fatal(err)
	}
	stage := filepath.Join(work, "project")
	writeTestFile(t, stage, "Files/chapter.rtf", "new", testTime)
	incoming, err := archive.Scan(ctx, stage)
	if err != nil {
		t.Fatal(err)
	}
	excluded, err := archive.CompareConflicts(before, incoming)
	if err != nil {
		t.Fatal(err)
	}
	j, err := a.preparePull(work, before, incoming, true, false, excluded)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.save(j); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(a.Local, j.Backup); err != nil {
		t.Fatal(err)
	}
	// A foreign or damaged final backup must be preserved and stop installation.
	writeTestFile(t, j.ExcludedBackup, "Settings/ui.plist", "damaged", testTime)
	if err := a.resumePull(ctx, j); err == nil {
		t.Fatal("damaged backup accepted")
	}
	old, err := archive.Scan(ctx, j.Backup)
	if err != nil || !archive.SameManifest(old, before, true) {
		t.Fatal("rollback copy lost")
	}
	if _, err := os.Stat(a.Local); !os.IsNotExist(err) {
		t.Fatal("installed despite backup failure")
	}
	if _, err := os.Stat(stage); err != nil {
		t.Fatal("incoming copy lost")
	}
	content, err := os.ReadFile(filepath.Join(j.ExcludedBackup, "Settings", "ui.plist"))
	if err != nil || string(content) != "damaged" {
		t.Fatal("foreign backup overwritten")
	}
}

func TestExcludedFilesWithoutConflictsCreateNoBackup(t *testing.T) {
	for _, same := range []bool{false, true} {
		t.Run(fmt.Sprint(same), func(t *testing.T) {
			d := newFake()
			a := testApp(t, d)
			remoteRoot := filepath.Join(t.TempDir(), "Project.scriv")
			writeTestFile(t, a.Local, "Files/chapter.rtf", "old", testTime.Add(-time.Hour))
			writeTestFile(t, remoteRoot, "Files/chapter.rtf", "new", testTime)
			localText := "older settings"
			if same {
				localText = "remote settings"
			}
			writeTestFile(t, a.Local, "Settings/ui.plist", localText, testTime.Add(-time.Hour))
			writeTestFile(t, remoteRoot, "Settings/ui.plist", "remote settings", testTime)
			zip := filepath.Join(t.TempDir(), "project.zip")
			if _, err := archive.Create(context.Background(), remoteRoot, zip); err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(zip)
			if err != nil {
				t.Fatal(err)
			}
			d.put(a.Remote, data)
			if err := a.Run(context.Background(), "pull"); err != nil {
				t.Fatal(err)
			}
			assertLocal(t, a, "new")
			assertNoPullBackup(t, a)
		})
	}
}
