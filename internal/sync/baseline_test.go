package sync

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"scrivsync/internal/archive"
)

func projectXML(title, cursor string) string {
	return `<ScrivenerProject Identifier="project-id" Version="2.0" Modified="today" Creator="Mac"><Binder><BinderItem UUID="item-id" Type="Text"><Title>` + title + `</Title><TextSettings><TextSelection>` + cursor + `</TextSelection></TextSettings></BinderItem></Binder></ScrivenerProject>`
}
func xmlArchive(t *testing.T, text string, when time.Time) []byte {
	t.Helper()
	root := filepath.Join(t.TempDir(), "Project.scriv")
	writeTestFile(t, root, "Project.scrivx", text, when)
	writeTestFile(t, root, "Files/chapter.rtf", "same content", testTime)
	target := filepath.Join(t.TempDir(), "project.zip")
	if _, err := archive.Create(context.Background(), root, target); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
func establishBaseline(t *testing.T, a *App, d *fakeDisk, text string) {
	t.Helper()
	d.put(a.Remote, xmlArchive(t, text, testTime))
	if err := a.Run(context.Background(), "pull"); err != nil {
		t.Fatal(err)
	}
	assertBaseline(t, a, text)
}
func assertBaseline(t *testing.T, a *App, text string) {
	t.Helper()
	folder := filepath.Join(a.State, "baseline")
	if _, err := readBaseline(context.Background(), folder); err != nil {
		t.Fatal(err)
	}
	actual, err := os.ReadFile(filepath.Join(folder, "project.scrivx"))
	if err != nil || string(actual) != text {
		t.Fatalf("wrong baseline: %v", err)
	}
}

func TestPullUsesSemanticBaselineAndPreservesSkippedLocalIndex(t *testing.T) {
	for _, sameMeaning := range []bool{false, true} {
		t.Run(fmt.Sprint(sameMeaning), func(t *testing.T) {
			d := newFake()
			a := testApp(t, d)
			establishBaseline(t, a, d, projectXML("Original", "0,0"))
			local := projectXML("Original", "123,0")
			writeTestFile(t, a.Local, "Project.scrivx", local, testTime.Add(2*time.Hour))
			title := "Remote rename"
			if sameMeaning {
				title = "Original"
			}
			incoming := projectXML(title, "500,0")
			d.put(a.Remote, xmlArchive(t, incoming, testTime.Add(time.Hour)))
			if err := a.Run(context.Background(), "pull"); err != nil {
				t.Fatal(err)
			}
			assertBaseline(t, a, incoming)
			installed, err := os.ReadFile(filepath.Join(a.Local, "Project.scrivx"))
			if err != nil || string(installed) != incoming {
				t.Fatal("archive XML was changed or not installed")
			}
			backups, _ := filepath.Glob(filepath.Join(a.State, "FileBackups", "*", "Project.scrivx"))
			if len(backups) != 1 {
				t.Fatalf("missing selective backup: %v", backups)
			}
			preserved, err := os.ReadFile(backups[0])
			if err != nil || string(preserved) != local {
				t.Fatal("local XML not backed up")
			}
		})
	}
}

func TestMeaningfulLocalChangesBlockEvenWhenRemoteFileIsNewer(t *testing.T) {
	for _, remoteTitle := range []string{"Original", "Remote rename"} {
		t.Run(remoteTitle, func(t *testing.T) {
			d := newFake()
			a := testApp(t, d)
			base := projectXML("Original", "0,0")
			establishBaseline(t, a, d, base)
			local := projectXML("Local rename", "0,0")
			writeTestFile(t, a.Local, "Project.scrivx", local, testTime.Add(time.Hour))
			d.put(a.Remote, xmlArchive(t, projectXML(remoteTitle, "1,0"), testTime.Add(2*time.Hour)))
			var conflict *archive.ConflictError
			if err := a.Run(context.Background(), "pull"); !errors.As(err, &conflict) {
				t.Fatalf("expected conflict: %v", err)
			}
			assertBaseline(t, a, base)
			actual, err := os.ReadFile(filepath.Join(a.Local, "Project.scrivx"))
			if err != nil || string(actual) != local {
				t.Fatal("local XML changed on conflict")
			}
		})
	}
}

func TestAbsentOrUnusableBaselineFallsBack(t *testing.T) {
	for _, mode := range []string{"absent", "corrupt", "different remote", "different identifier"} {
		t.Run(mode, func(t *testing.T) {
			d := newFake()
			a := testApp(t, d)
			base := projectXML("Original", "0,0")
			establishBaseline(t, a, d, base)
			folder := filepath.Join(a.State, "baseline")
			switch mode {
			case "absent":
				if err := os.RemoveAll(folder); err != nil {
					t.Fatal(err)
				}
			case "corrupt":
				if err := os.WriteFile(filepath.Join(folder, "project.scrivx"), []byte("corrupt"), 0600); err != nil {
					t.Fatal(err)
				}
			case "different remote":
				a.Remote = "app:/another.zip"
			case "different identifier":
				data := strings.ReplaceAll(base, "project-id", "another-id")
				meta, err := readBaseline(context.Background(), folder)
				if err != nil {
					t.Fatal(err)
				}
				meta.Meta.Identifier = "another-id"
				meta.Meta.SHA256 = bytesHash([]byte(data))
				encoded, err := json.Marshal(meta.Meta)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(folder, "meta.json"), encoded, 0600); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(folder, "project.scrivx"), []byte(data), 0600); err != nil {
					t.Fatal(err)
				}
			}
			writeTestFile(t, a.Local, "Project.scrivx", projectXML("Original", "4,0"), testTime.Add(2*time.Hour))
			d.put(a.Remote, xmlArchive(t, projectXML("Remote", "0,0"), testTime.Add(time.Hour)))
			var conflict *archive.ConflictError
			if err := a.Run(context.Background(), "pull"); !errors.As(err, &conflict) {
				t.Fatalf("unsafe fallback: %v", err)
			}
		})
	}
}

func TestIdenticalPullCreatesBaselineWithoutReplacingLocalFiles(t *testing.T) {
	d := newFake()
	a := testApp(t, d)
	text := projectXML("Original", "0,0")
	writeTestFile(t, a.Local, "Project.scrivx", text, testTime.Add(time.Hour))
	writeTestFile(t, a.Local, "Files/chapter.rtf", "same content", testTime)
	d.put(a.Remote, xmlArchive(t, text, testTime))
	if err := a.Run(context.Background(), "pull"); err != nil {
		t.Fatal(err)
	}
	assertBaseline(t, a, text)
	info, err := os.Stat(filepath.Join(a.Local, "Project.scrivx"))
	if err != nil || !info.ModTime().Equal(testTime.Add(time.Hour)) {
		t.Fatal("identical pull replaced local file")
	}
	assertNoPullBackup(t, a)
}

func TestPushBaselineComesFromPublishedSnapshotOnRecovery(t *testing.T) {
	d := newFake()
	a := testApp(t, d)
	base := projectXML("Original", "0,0")
	establishBaseline(t, a, d, base)
	sent := projectXML("Sent snapshot", "2,0")
	writeTestFile(t, a.Local, "Project.scrivx", sent, testTime.Add(time.Hour))
	d.failUpload = true
	if err := a.Run(context.Background(), "push"); err == nil {
		t.Fatal("missing upload failure")
	}
	assertBaseline(t, a, base)
	writeTestFile(t, a.Local, "Project.scrivx", projectXML("Later local edit", "0,0"), testTime.Add(2*time.Hour))
	d.failUpload = false
	if err := a.Run(context.Background(), "push"); err != nil {
		t.Fatal(err)
	}
	assertBaseline(t, a, sent)
	actual, err := os.ReadFile(filepath.Join(a.Local, "Project.scrivx"))
	if err != nil || !strings.Contains(string(actual), "Later local edit") {
		t.Fatal("working project overwritten")
	}
}

func TestMeaningEqualWithoutBaselineAndDifferentProjects(t *testing.T) {
	for _, differentID := range []bool{false, true} {
		t.Run(fmt.Sprint(differentID), func(t *testing.T) {
			d := newFake()
			a := testApp(t, d)
			local := projectXML("Original", "10,0")
			writeTestFile(t, a.Local, "Project.scrivx", local, testTime.Add(2*time.Hour))
			writeTestFile(t, a.Local, "Files/chapter.rtf", "same content", testTime)
			incoming := projectXML("Original", "0,0")
			if differentID {
				incoming = strings.ReplaceAll(incoming, "project-id", "different-id")
			}
			d.put(a.Remote, xmlArchive(t, incoming, testTime.Add(3*time.Hour)))
			err := a.Run(context.Background(), "pull")
			if differentID {
				var conflict *archive.ConflictError
				if !errors.As(err, &conflict) {
					t.Fatalf("project identity mismatch accepted: %v", err)
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				assertBaseline(t, a, incoming)
			}
		})
	}
}
