package sync

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

func TestForcePullBacksUpWholeProjectAndRetainsReport(t *testing.T) {
	for _, recoverInstall := range []bool{false, true} {
		name := "success"
		if recoverInstall {
			name = "recover"
		}
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			d := newFake()
			a := testApp(t, d)
			a.ForcePull = true
			writeTestFile(t, a.Local, "Files/chapter.rtf", "local", testTime.Add(time.Hour))
			writeTestFile(t, a.Local, "unique/content.rtf", "unique", testTime)
			writeTestFile(t, a.Local, "Settings/ui.ini", "preferences", testTime)
			before, err := archive.Scan(ctx, a.Local)
			if err != nil {
				t.Fatal(err)
			}
			d.put(a.Remote, archiveBytes(t, "remote", testTime))
			if recoverInstall {
				checks := 0
				a.CheckClosed = func(context.Context) error {
					checks++
					if checks == 2 {
						return errors.New("simulated interruption before install")
					}
					return nil
				}
				if err := a.Run(ctx, "pull"); err == nil {
					t.Fatal("expected interruption")
				}
				assertLocal(t, a, "local")
				a.CheckClosed = nil
				a.ForcePull = false // The journal already records the approved installation.
			}
			if err := a.Run(ctx, "pull"); err != nil {
				t.Fatal(err)
			}
			assertLocal(t, a, "remote")
			backups, _ := filepath.Glob(filepath.Join(a.State, "Backups", "*"))
			if len(backups) != 1 {
				t.Fatalf("backups: %v", backups)
			}
			backup, err := archive.Scan(ctx, backups[0])
			if err != nil || !archive.SameManifest(before, backup, true) {
				t.Fatalf("incomplete backup: %v", err)
			}
			if _, err := os.Stat(filepath.Join(a.Local, "unique")); !os.IsNotExist(err) {
				t.Fatal("local-only content left in installed project")
			}
			runs, _ := filepath.Glob(filepath.Join(a.State, "run-*"))
			if len(runs) != 1 {
				t.Fatalf("report run lost: %v", runs)
			}
			for _, path := range []string{"project.zip", "report/index.txt"} {
				if _, err := os.Stat(filepath.Join(runs[0], path)); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := os.Stat(a.journalPath()); !os.IsNotExist(err) {
				t.Fatal("completed journal retained")
			}
		})
	}
}

func TestForcePullBackupCollisionLeavesLocalUntouched(t *testing.T) {
	d := newFake()
	a := testApp(t, d)
	a.ForcePull = true
	writeTestFile(t, a.Local, "Files/chapter.rtf", "local", testTime.Add(time.Hour))
	d.put(a.Remote, archiveBytes(t, "remote", testTime))
	backup := filepath.Join(a.State, "Backups", filepath.Base(a.Local)+"_"+testTime.Format("2006-01-02T15-04-05Z"))
	writeTestFile(t, backup, "keep.txt", "previous backup", testTime)
	if err := a.Run(context.Background(), "pull"); err == nil || !strings.Contains(err.Error(), "имя локального бэкапа уже занято") {
		t.Fatalf("unexpected result: %v", err)
	}
	assertLocal(t, a, "local")
	data, err := os.ReadFile(filepath.Join(backup, "keep.txt"))
	if err != nil || string(data) != "previous backup" {
		t.Fatal("existing backup overwritten")
	}
}

func TestPullOnlyUIChangeDoesNotBlockAndIsBackedUp(t *testing.T) {
	d := newFake()
	a := testApp(t, d)
	writeTestFile(t, a.Local, "Files/chapter.rtf", "same", testTime)
	writeTestFile(t, a.Local, "Settings/ui.ini", "preferences", testTime.Add(time.Hour))
	d.put(a.Remote, archiveBytes(t, "same", testTime))
	if err := a.Run(context.Background(), "pull"); err != nil {
		t.Fatal(err)
	}
	backups, _ := filepath.Glob(filepath.Join(a.State, "Backups", "*", "Settings", "ui.ini"))
	if len(backups) != 1 {
		t.Fatal("UI preferences not backed up")
	}
}
