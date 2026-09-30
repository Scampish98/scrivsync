package sync

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"scrivsync/internal/archive"
	"scrivsync/internal/yandex"
)

var testTime = time.Date(2026, 9, 30, 14, 30, 0, 0, time.UTC)

func writeTestFile(t *testing.T, root, rel, text string, when time.Time) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(text), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(p, when, when); err != nil {
		t.Fatal(err)
	}
}

type fakeObject struct {
	meta yandex.Resource
	data []byte
}
type fakeDisk struct {
	files          map[string]fakeObject
	moves, uploads int
	failMove       int
	failAfter      bool
	failUpload     bool
	afterUpload    func()
	afterDownload  func()
}

func newFake() *fakeDisk { return &fakeDisk{files: map[string]fakeObject{}} }
func (d *fakeDisk) put(p string, b []byte) {
	h := sha256.Sum256(b)
	d.files[p] = fakeObject{yandex.Resource{Type: "file", Size: int64(len(b)), SHA256: hex.EncodeToString(h[:]), Created: testTime, Modified: testTime, ID: p}, append([]byte(nil), b...)}
}
func (d *fakeDisk) Stat(_ context.Context, p string) (*yandex.Resource, error) {
	o, ok := d.files[p]
	if !ok {
		return nil, nil
	}
	r := o.meta
	return &r, nil
}
func (d *fakeDisk) Mkdir(context.Context, string) error { return nil }
func (d *fakeDisk) Upload(_ context.Context, local, remote string) error {
	d.uploads++
	if d.failUpload {
		return errors.New("simulated upload failure")
	}
	if _, ok := d.files[remote]; ok {
		return errors.New("exists")
	}
	b, err := os.ReadFile(local)
	if err != nil {
		return err
	}
	d.put(remote, b)
	if d.afterUpload != nil {
		d.afterUpload()
	}
	return nil
}
func (d *fakeDisk) Download(_ context.Context, remote, local string) error {
	o, ok := d.files[remote]
	if !ok {
		return errors.New("missing")
	}
	if err := os.WriteFile(local, o.data, 0600); err != nil {
		return err
	}
	if d.afterDownload != nil {
		d.afterDownload()
	}
	return nil
}
func (d *fakeDisk) Move(_ context.Context, from, to string) error {
	d.moves++
	if d.moves == d.failMove && !d.failAfter {
		return errors.New("simulated move failure")
	}
	o, ok := d.files[from]
	if !ok {
		return errors.New("source missing")
	}
	if _, ok := d.files[to]; ok {
		return errors.New("destination exists")
	}
	d.files[to] = o
	delete(d.files, from)
	if d.moves == d.failMove && d.failAfter {
		return errors.New("response lost after successful move")
	}
	return nil
}

func testApp(t *testing.T, d *fakeDisk) *App {
	t.Helper()
	base := t.TempDir()
	state := filepath.Join(base, ".Project.scriv.scrivsync-state")
	if err := os.Mkdir(state, 0700); err != nil {
		t.Fatal(err)
	}
	return &App{Disk: d, Local: filepath.Join(base, "Project.scriv"), Remote: "disk:/Scrivener/Project.zip", State: state, Out: &bytes.Buffer{}, Now: func() time.Time { return testTime }}
}
func archiveBytes(t *testing.T, text string, when time.Time) []byte {
	t.Helper()
	base := t.TempDir()
	root := filepath.Join(base, "Project.scriv")
	writeTestFile(t, root, "Files/chapter.rtf", text, when)
	p := filepath.Join(base, "project.zip")
	if _, err := archive.Create(context.Background(), root, p); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
func assertLocal(t *testing.T, a *App, want string) {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(a.Local, "Files", "chapter.rtf"))
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != want {
		t.Fatalf("local content %q, want %q", b, want)
	}
}

func TestPushFirstAndBackup(t *testing.T) {
	for _, existing := range []bool{false, true} {
		t.Run(fmt.Sprint(existing), func(t *testing.T) {
			d := newFake()
			a := testApp(t, d)
			writeTestFile(t, a.Local, "Files/chapter.rtf", "new", testTime)
			old := archiveBytes(t, "old", testTime.Add(-time.Hour))
			if existing {
				d.put(a.Remote, old)
			}
			if err := a.push(context.Background()); err != nil {
				t.Fatal(err)
			}
			if _, ok := d.files[a.Remote]; !ok {
				t.Fatal("no current archive")
			}
			if existing && !bytes.Equal(d.files[backupPath(a.Remote, testTime)].data, old) {
				t.Fatal("old archive not preserved")
			}
			if _, err := os.Stat(a.journalPath()); !os.IsNotExist(err) {
				t.Fatal("journal not cleared")
			}
		})
	}
}

func TestBackupCollisionNoChanges(t *testing.T) {
	d := newFake()
	a := testApp(t, d)
	writeTestFile(t, a.Local, "Files/chapter.rtf", "new", testTime)
	d.put(a.Remote, []byte("old"))
	bp := backupPath(a.Remote, testTime)
	d.put(bp, []byte("existing backup"))
	err := a.push(context.Background())
	if err == nil || !strings.Contains(err.Error(), "имя бэкапа уже занято") {
		t.Fatalf("wrong error %v", err)
	}
	if d.uploads != 0 || d.moves != 0 || string(d.files[a.Remote].data) != "old" || string(d.files[bp].data) != "existing backup" {
		t.Fatal("collision modified remote files")
	}
}

func TestPushRecoveryBeforeAndAfterBothMoves(t *testing.T) {
	for _, n := range []int{1, 2} {
		for _, after := range []bool{false, true} {
			t.Run(fmt.Sprintf("move%d_after%v", n, after), func(t *testing.T) {
				d := newFake()
				a := testApp(t, d)
				writeTestFile(t, a.Local, "Files/chapter.rtf", "new", testTime)
				old := archiveBytes(t, "old", testTime.Add(-time.Hour))
				d.put(a.Remote, old)
				d.failMove = n
				d.failAfter = after
				if err := a.push(context.Background()); err == nil {
					t.Fatal("failure not injected")
				}
				d.failMove = 0
				recovered, err := a.recover(context.Background(), "push")
				if !recovered || err != nil {
					t.Fatalf("recovery: %v %v", recovered, err)
				}
				if !bytes.Equal(d.files[backupPath(a.Remote, testTime)].data, old) {
					t.Fatal("lost backup")
				}
				if bytes.Equal(d.files[a.Remote].data, old) {
					t.Fatal("new archive not installed")
				}
				if d.uploads != 1 {
					t.Fatalf("uploaded %d times", d.uploads)
				}
			})
		}
	}
}

func TestFailedUploadKeepsCurrentAndCanResume(t *testing.T) {
	d := newFake()
	a := testApp(t, d)
	writeTestFile(t, a.Local, "Files/chapter.rtf", "new", testTime)
	d.put(a.Remote, []byte("old"))
	d.failUpload = true
	if err := a.push(context.Background()); err == nil {
		t.Fatal("expected failure")
	}
	if string(d.files[a.Remote].data) != "old" || d.moves != 0 {
		t.Fatal("old archive moved prematurely")
	}
	d.failUpload = false
	if ok, err := a.recover(context.Background(), "push"); !ok || err != nil {
		t.Fatalf("%v %v", ok, err)
	}
}

func TestPushDetectsRemoteChange(t *testing.T) {
	d := newFake()
	a := testApp(t, d)
	writeTestFile(t, a.Local, "Files/chapter.rtf", "new", testTime)
	d.put(a.Remote, []byte("old"))
	d.afterUpload = func() { d.put(a.Remote, []byte("changed elsewhere")) }
	if err := a.push(context.Background()); err == nil {
		t.Fatal("remote change ignored")
	}
	if d.moves != 0 || string(d.files[a.Remote].data) != "changed elsewhere" {
		t.Fatal("overwrote changed remote")
	}
}

func TestPullInstallsAndBacksUp(t *testing.T) {
	d := newFake()
	a := testApp(t, d)
	writeTestFile(t, a.Local, "Files/chapter.rtf", "old", testTime.Add(-time.Hour))
	d.put(a.Remote, archiveBytes(t, "new", testTime))
	if err := a.pull(context.Background()); err != nil {
		t.Fatal(err)
	}
	assertLocal(t, a, "new")
	backups, err := os.ReadDir(filepath.Join(a.State, "Backups"))
	if err != nil || len(backups) != 1 {
		t.Fatalf("backups: %v %v", backups, err)
	}
	b, err := os.ReadFile(filepath.Join(a.State, "Backups", backups[0].Name(), "Files", "chapter.rtf"))
	if err != nil || string(b) != "old" {
		t.Fatal("backup content lost")
	}
	fi, err := os.Stat(filepath.Join(a.Local, "Files", "chapter.rtf"))
	if err != nil || !fi.ModTime().Equal(testTime) {
		t.Fatal("mtime not restored")
	}
}

func TestPullConflictLeavesProjectUntouched(t *testing.T) {
	for _, mode := range []string{"newer", "equal", "only-local"} {
		t.Run(mode, func(t *testing.T) {
			d := newFake()
			a := testApp(t, d)
			when := testTime
			if mode == "newer" {
				when = when.Add(time.Hour)
			}
			writeTestFile(t, a.Local, "Files/chapter.rtf", "local", when)
			if mode == "only-local" {
				writeTestFile(t, a.Local, "note.rtf", "keep", testTime)
			}
			d.put(a.Remote, archiveBytes(t, "remote", testTime))
			before, err := archive.Scan(context.Background(), a.Local)
			if err != nil {
				t.Fatal(err)
			}
			var ce *archive.ConflictError
			if err := a.pull(context.Background()); !errors.As(err, &ce) {
				t.Fatalf("expected conflict, got %v", err)
			}
			after, err := archive.Scan(context.Background(), a.Local)
			if err != nil || !archive.SameManifest(before, after, true) {
				t.Fatal("conflict modified project")
			}
		})
	}
}

func TestPullNewProjectAndIdenticalProject(t *testing.T) {
	d := newFake()
	a := testApp(t, d)
	d.put(a.Remote, archiveBytes(t, "text", testTime))
	if err := a.pull(context.Background()); err != nil {
		t.Fatal(err)
	}
	assertLocal(t, a, "text")
	if err := a.pull(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(a.State, "Backups")); !os.IsNotExist(err) {
		t.Fatal("identical pull created backup")
	}
}

func TestPullDetectsLocalChangesDuringDownload(t *testing.T) {
	d := newFake()
	a := testApp(t, d)
	writeTestFile(t, a.Local, "Files/chapter.rtf", "old", testTime.Add(-time.Hour))
	d.put(a.Remote, archiveBytes(t, "remote", testTime))
	d.afterDownload = func() { writeTestFile(t, a.Local, "Files/chapter.rtf", "edited during pull", testTime.Add(time.Hour)) }
	if err := a.pull(context.Background()); err == nil {
		t.Fatal("local change ignored")
	}
	assertLocal(t, a, "edited during pull")
}

func TestPullDownloadChecksumMismatch(t *testing.T) {
	d := newFake()
	a := testApp(t, d)
	writeTestFile(t, a.Local, "Files/chapter.rtf", "keep", testTime)
	d.put(a.Remote, archiveBytes(t, "new", testTime.Add(time.Hour)))
	o := d.files[a.Remote]
	o.data = []byte("corrupt")
	d.files[a.Remote] = o
	if err := a.pull(context.Background()); err == nil {
		t.Fatal("checksum mismatch ignored")
	}
	assertLocal(t, a, "keep")
}

func TestPullRecoveryAcrossRenames(t *testing.T) {
	for _, phase := range []int{0, 1, 2} {
		t.Run(fmt.Sprint(phase), func(t *testing.T) {
			d := newFake()
			a := testApp(t, d)
			ctx := context.Background()
			writeTestFile(t, a.Local, "Files/chapter.rtf", "old", testTime.Add(-time.Hour))
			old, err := archive.Scan(ctx, a.Local)
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
			backup := filepath.Join(a.State, "Backups", "Project.scriv_2026-09-30T14-30-00Z")
			if err := os.MkdirAll(filepath.Dir(backup), 0700); err != nil {
				t.Fatal(err)
			}
			j := &Journal{Version: 1, Command: "pull", Phase: "install", Local: a.Local, Remote: a.Remote, Work: work, Backup: backup, OldLocal: old, NewLocal: incoming}
			if err := a.save(j); err != nil {
				t.Fatal(err)
			}
			if phase >= 1 {
				if err := os.Rename(a.Local, backup); err != nil {
					t.Fatal(err)
				}
			}
			if phase == 2 {
				if err := os.Rename(stage, a.Local); err != nil {
					t.Fatal(err)
				}
			}
			if ok, err := a.recover(ctx, "pull"); !ok || err != nil {
				t.Fatalf("recovery: %v %v", ok, err)
			}
			assertLocal(t, a, "new")
			b, err := os.ReadFile(filepath.Join(backup, "Files", "chapter.rtf"))
			if err != nil || string(b) != "old" {
				t.Fatal("old content not preserved")
			}
		})
	}
}

func TestBackupNameHasOnlyCreationTimestamp(t *testing.T) {
	when := time.Date(2026, 9, 30, 17, 30, 0, 0, time.FixedZone("MSK", 3*3600))
	if got := backupPath("disk:/Folder/Project.zip", when); got != "disk:/Folder/Backups/Project_2026-09-30T14-30-00Z.zip" {
		t.Fatal(got)
	}
}
