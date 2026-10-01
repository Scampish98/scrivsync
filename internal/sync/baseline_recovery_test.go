package sync

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"scrivsync/internal/archive"
)

func prepareBaselinePull(t *testing.T, a *App, incoming string) *Journal {
	t.Helper()
	ctx := context.Background()
	before, err := archive.Scan(ctx, a.Local)
	if err != nil {
		t.Fatal(err)
	}
	work, err := os.MkdirTemp(a.State, "run-")
	if err != nil {
		t.Fatal(err)
	}
	zip := filepath.Join(work, "project.zip")
	if err := os.WriteFile(zip, xmlArchive(t, incoming, testTime.Add(time.Hour)), 0600); err != nil {
		t.Fatal(err)
	}
	after, err := archive.Extract(ctx, zip, filepath.Join(work, "project"))
	if err != nil {
		t.Fatal(err)
	}
	j, err := a.preparePull(work, before, after, true, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.prepareBaseline(ctx, j); err != nil {
		t.Fatal(err)
	}
	if err := a.save(j); err != nil {
		t.Fatal(err)
	}
	return j
}

func TestBaselineRecoveryAcrossProjectAndBaselineRenames(t *testing.T) {
	for phase := 0; phase <= 4; phase++ {
		t.Run(fmt.Sprint(phase), func(t *testing.T) {
			d := newFake()
			a := testApp(t, d)
			establishBaseline(t, a, d, projectXML("Original", "0,0"))
			incoming := projectXML("Incoming", "0,0")
			j := prepareBaselinePull(t, a, incoming)
			if phase >= 1 {
				if err := os.Rename(a.Local, j.Backup); err != nil {
					t.Fatal(err)
				}
			}
			if phase >= 2 {
				if err := os.Rename(filepath.Join(j.Work, "project"), a.Local); err != nil {
					t.Fatal(err)
				}
			}
			if phase >= 3 {
				if err := os.Rename(filepath.Join(a.State, "baseline"), filepath.Join(j.Work, "previous-baseline")); err != nil {
					t.Fatal(err)
				}
			}
			if phase >= 4 {
				if err := os.Rename(filepath.Join(j.Work, "baseline"), filepath.Join(a.State, "baseline")); err != nil {
					t.Fatal(err)
				}
			}
			if err := a.Run(context.Background(), "pull"); err != nil {
				t.Fatal(err)
			}
			assertBaseline(t, a, incoming)
			actual, err := os.ReadFile(filepath.Join(a.Local, "Project.scrivx"))
			if err != nil || string(actual) != incoming {
				t.Fatal("wrong installed project")
			}
			if _, err := os.Stat(a.journalPath()); !os.IsNotExist(err) {
				t.Fatal("journal retained")
			}
			assertNoPullBackup(t, a)
		})
	}
}

func TestBaselineFailureRetainsJournalAndCanResume(t *testing.T) {
	d := newFake()
	a := testApp(t, d)
	original := projectXML("Original", "0,0")
	establishBaseline(t, a, d, original)
	incoming := projectXML("Incoming", "0,0")
	j := prepareBaselinePull(t, a, incoming)
	// An unexpected destination must stop baseline rotation without losing either version.
	barrier := filepath.Join(j.Work, "previous-baseline")
	if err := os.Mkdir(barrier, 0700); err != nil {
		t.Fatal(err)
	}
	if err := a.resumePull(context.Background(), j); err == nil {
		t.Fatal("baseline error ignored")
	}
	assertBaseline(t, a, original)
	if _, err := os.Stat(a.journalPath()); err != nil {
		t.Fatal("unfinished journal lost")
	}
	if _, err := os.Stat(j.Backup); err != nil {
		t.Fatal("rollback project lost")
	}
	if err := os.Remove(barrier); err != nil {
		t.Fatal(err)
	}
	if err := a.Run(context.Background(), "pull"); err != nil {
		t.Fatal(err)
	}
	assertBaseline(t, a, incoming)
}

func TestForcePullUpdatesBaselineAndKeepsFullBackup(t *testing.T) {
	d := newFake()
	a := testApp(t, d)
	establishBaseline(t, a, d, projectXML("Original", "0,0"))
	local := projectXML("Local edit", "0,0")
	writeTestFile(t, a.Local, "Project.scrivx", local, testTime.Add(2*time.Hour))
	incoming := projectXML("Remote edit", "0,0")
	d.put(a.Remote, xmlArchive(t, incoming, testTime.Add(time.Hour)))
	a.ForcePull = true
	if err := a.Run(context.Background(), "pull"); err != nil {
		t.Fatal(err)
	}
	assertBaseline(t, a, incoming)
	backups, _ := filepath.Glob(filepath.Join(a.State, "Backups", "*", "Project.scrivx"))
	if len(backups) != 1 {
		t.Fatalf("backup missing: %v", backups)
	}
	data, err := os.ReadFile(backups[0])
	if err != nil || string(data) != local {
		t.Fatal("local changes lost")
	}
}

func TestPreparedBaselineMustMatchInstalledSnapshot(t *testing.T) {
	d := newFake()
	a := testApp(t, d)
	establishBaseline(t, a, d, projectXML("Original", "0,0"))
	j := prepareBaselinePull(t, a, projectXML("Incoming", "0,0"))
	j.Baseline.SHA256 = bytesHash([]byte("another version"))
	if err := validateBaselineJournal(j); err == nil {
		t.Fatal("unrelated baseline accepted")
	}
}
