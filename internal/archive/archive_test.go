package archive

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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

func TestArchiveRoundTripPreservesContentsAndDates(t *testing.T) {
	ctx := context.Background()
	base := t.TempDir()
	project := filepath.Join(base, "Мой роман.scriv")
	writeTestFile(t, project, "Files/Глава 1.rtf", "текст главы", testTime.Add(789*time.Millisecond))
	writeTestFile(t, project, "Project.scrivx", "<project/>", testTime.Add(-time.Hour))
	if err := os.Mkdir(filepath.Join(project, "Empty"), 0755); err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(base, "project.zip")
	before, err := Create(ctx, project, archive)
	if err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(base, "restored")
	after, err := Extract(ctx, archive, dest)
	if err != nil {
		t.Fatal(err)
	}
	if !SameManifest(before, after, false) {
		t.Fatal("round trip changed project contents")
	}
	for p, e := range before {
		if e.Dir {
			continue
		}
		got := after[p].Modified
		if !got.Equal(e.Modified.Truncate(time.Second)) {
			t.Fatalf("%s: got %s, want original timestamp %s", p, got, e.Modified)
		}
	}
	if err := CheckConflicts(before, after); err != nil {
		t.Fatalf("timestamp rounding caused false conflict: %v", err)
	}
}

func TestConflictRules(t *testing.T) {
	for _, tc := range []struct {
		name              string
		local, incoming   Entry
		missing, conflict bool
	}{
		{"identical newer local", Entry{Hash: "same", Modified: testTime.Add(time.Hour)}, Entry{Hash: "same", Modified: testTime}, false, false},
		{"local newer", Entry{Hash: "local", Modified: testTime.Add(3 * time.Second)}, Entry{Hash: "remote", Modified: testTime}, false, true},
		{"exactly two seconds remote newer", Entry{Hash: "local", Modified: testTime}, Entry{Hash: "remote", Modified: testTime.Add(2 * time.Second)}, false, true},
		{"exactly two seconds local newer", Entry{Hash: "local", Modified: testTime.Add(2 * time.Second)}, Entry{Hash: "remote", Modified: testTime}, false, true},
		{"same date different bytes", Entry{Hash: "local", Modified: testTime}, Entry{Hash: "remote", Modified: testTime}, false, true},
		{"remote newer", Entry{Hash: "local", Modified: testTime}, Entry{Hash: "remote", Modified: testTime.Add(3 * time.Second)}, false, false},
		{"only local", Entry{Hash: "local", Modified: testTime}, Entry{}, true, true},
		{"type change", Entry{Dir: true}, Entry{Hash: "remote"}, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := Manifest{}
			if !tc.missing {
				r["chapter.rtf"] = tc.incoming
			}
			err := CheckConflicts(Manifest{"chapter.rtf": tc.local}, r)
			if (err != nil) != tc.conflict {
				t.Fatalf("unexpected result: %v", err)
			}
			if err != nil {
				var ce *ConflictError
				if !errors.As(err, &ce) || !strings.Contains(err.Error(), "chapter.rtf") {
					t.Fatalf("missing conflict details: %v", err)
				}
			}
		})
	}
	if err := CheckConflicts(Manifest{}, Manifest{"new.rtf": {Hash: "new"}}); err != nil {
		t.Fatal(err)
	}
}

func rawZIP(t *testing.T, names []string, symlink bool) []byte {
	t.Helper()
	var b bytes.Buffer
	z := zip.NewWriter(&b)
	for _, name := range names {
		h := &zip.FileHeader{Name: name, Modified: testTime, Method: zip.Store}
		h.SetMode(0644)
		if symlink {
			h.SetMode(os.ModeSymlink | 0777)
		}
		if strings.HasSuffix(name, "/") {
			h.SetMode(os.ModeDir | 0755)
		}
		w, err := z.CreateHeader(h)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.HasSuffix(name, "/") {
			if _, err = w.Write([]byte("payload-for-crc")); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := z.Close(); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func TestRejectUnsafeArchives(t *testing.T) {
	for _, tc := range []struct {
		name  string
		paths []string
		link  bool
	}{
		{"traversal", []string{"Project.scriv/../../escaped"}, false},
		{"absolute", []string{"/escaped"}, false},
		{"backslash", []string{"Project.scriv/..\\escaped"}, false},
		{"multiple roots", []string{"One/a", "Two/b"}, false},
		{"duplicate", []string{"Project/a", "Project/a"}, false},
		{"case collision", []string{"Project/A/file", "Project/a/other"}, false},
		{"symlink", []string{"Project/link"}, true},
		{"file as directory", []string{"Project/a", "Project/a/b"}, false},
		{"windows device", []string{"Project/CON.txt"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			base := t.TempDir()
			p := filepath.Join(base, "input.zip")
			if err := os.WriteFile(p, rawZIP(t, tc.paths, tc.link), 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := Extract(context.Background(), p, filepath.Join(base, "out")); err == nil {
				t.Fatal("unsafe archive accepted")
			}
		})
	}
}

func TestCorruptZIPRejected(t *testing.T) {
	b := rawZIP(t, []string{"Project/a"}, false)
	i := bytes.Index(b, []byte("payload-for-crc"))
	if i < 0 {
		t.Fatal("payload missing")
	}
	b[i] = 'X'
	base := t.TempDir()
	p := filepath.Join(base, "bad.zip")
	if err := os.WriteFile(p, b, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Extract(context.Background(), p, filepath.Join(base, "out")); err == nil {
		t.Fatal("CRC corruption accepted")
	}
}

func TestArchiveRefusesSymlinks(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "Project.scriv")
	writeTestFile(t, root, "chapter", "text", testTime)
	if err := os.Symlink("chapter", filepath.Join(root, "link")); err != nil {
		t.Skip(err)
	}
	if _, err := Create(context.Background(), root, filepath.Join(base, "out.zip")); err == nil {
		t.Fatal("source symlink accepted")
	}
}
