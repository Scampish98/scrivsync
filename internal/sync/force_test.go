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
			if _, err := os.Stat(filepath.Join(a.State, "FileBackups")); !os.IsNotExist(err) {
				t.Fatal("full backup duplicated by selective backup")
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

func TestPullExcludedConflictsCreateSelectiveBackup(t *testing.T) {
	for _, remoteHasGenerated := range []bool{false, true} {
		name := "local-only"
		if remoteHasGenerated {
			name = "local-newer"
		}
		t.Run(name, func(t *testing.T) {
			d := newFake()
			a := testApp(t, d)
			writeTestFile(t, a.Local, "Files/chapter.rtf", "same", testTime)
			paths := []string{
				"Settings/ui.ini", "Settings/ui.plist", "Settings/ui-common.xml",
				"Settings/recents.txt", "Settings/favorites.xml", "Settings/templateinfo.xml",
				"Files/search.indexes", "Files/binder.autosave", "Files/binder.backup",
				"Files/Data/docs.checksum", "QuickLook/Preview.html", "QuickLook/cache/Thumbnail.jpg",
			}
			for _, path := range paths {
				writeTestFile(t, a.Local, path, "local state", testTime.Add(time.Hour))
			}

			remoteRoot := filepath.Join(t.TempDir(), "Project.scriv")
			writeTestFile(t, remoteRoot, "Files/chapter.rtf", "same", testTime)
			if remoteHasGenerated {
				for _, path := range paths {
					writeTestFile(t, remoteRoot, path, "remote state", testTime)
				}
			}
			zipPath := filepath.Join(t.TempDir(), "project.zip")
			if _, err := archive.Create(context.Background(), remoteRoot, zipPath); err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(zipPath)
			if err != nil {
				t.Fatal(err)
			}
			d.put(a.Remote, data)
			if err := a.Run(context.Background(), "pull"); err != nil {
				t.Fatal(err)
			}
			assertLocal(t, a, "same")
			backups, err := filepath.Glob(filepath.Join(a.State, "FileBackups", "*"))
			if err != nil || len(backups) != 1 {
				t.Fatalf("selective backups: %v, %v", backups, err)
			}
			backup, err := archive.Scan(context.Background(), backups[0])
			if err != nil {
				t.Fatal(err)
			}
			if len(excludedFilePaths(backup)) != len(paths) {
				t.Fatalf("unexpected backup files: %v", backup)
			}
			for _, path := range paths {
				entry := backup[path]
				content, err := os.ReadFile(filepath.Join(backups[0], filepath.FromSlash(path)))
				if err != nil || string(content) != "local state" || !entry.Modified.Equal(testTime.Add(time.Hour)) {
					t.Fatalf("local backup lost: %s, %v", path, err)
				}
			}
			if _, err := os.Stat(filepath.Join(a.State, "Backups")); !os.IsNotExist(err) {
				t.Fatal("unexpected full project backup")
			}
			runs, _ := filepath.Glob(filepath.Join(a.State, "run-*"))
			if len(runs) != 0 {
				t.Fatal("temporary rollback not cleaned up")
			}
			for _, path := range paths {
				data, err := os.ReadFile(filepath.Join(a.Local, filepath.FromSlash(path)))
				if remoteHasGenerated {
					if err != nil || string(data) != "remote state" {
						t.Fatalf("archive setting not installed: %s, %v", path, err)
					}
				} else if !os.IsNotExist(err) {
					t.Fatalf("local-only setting left behind: %s, %v", path, err)
				}
			}
		})
	}
}

func assertNoPullBackup(t *testing.T, a *App) {
	t.Helper()
	if _, err := os.Stat(filepath.Join(a.State, "Backups")); !os.IsNotExist(err) {
		t.Fatalf("unexpected permanent backup: %v", err)
	}
	if _, err := os.Stat(filepath.Join(a.State, "FileBackups")); !os.IsNotExist(err) {
		t.Fatalf("unexpected selective backup: %v", err)
	}
	runs, err := filepath.Glob(filepath.Join(a.State, "run-*"))
	if err != nil || len(runs) != 0 {
		t.Fatalf("temporary rollback data not cleaned: %v, %v", runs, err)
	}
}
