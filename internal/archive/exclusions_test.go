package archive

import (
	"testing"
	"time"
)

func TestExcludedFilesUseOrdinaryConflictRulesForBackups(t *testing.T) {
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	paths := append([]string(nil), pullExclusions.Files...)
	paths = append(paths, "QuickLook/Preview.html", "QuickLook/cache/Thumbnail.jpg")
	for _, p := range paths {
		for _, tc := range []struct {
			name   string
			exists bool
			hash   string
			delta  time.Duration
			backup bool
		}{
			{"local-only", false, "", 0, true},
			{"identical", true, "local", -time.Hour, false},
			{"newer-local", true, "remote", -time.Hour, true},
			{"equal-time", true, "remote", 0, true},
			{"two-seconds", true, "remote", 2 * time.Second, true},
			{"newer-remote", true, "remote", 3 * time.Second, false},
		} {
			t.Run(p+"/"+tc.name, func(t *testing.T) {
				local := Manifest{p: {Hash: "local", Modified: now}}
				incoming := Manifest{}
				if tc.exists {
					incoming[p] = Entry{Hash: tc.hash, Modified: now.Add(tc.delta)}
				}
				excluded, err := CompareConflicts(local, incoming)
				_, saved := excluded[p]
				if err != nil || saved != tc.backup {
					t.Fatalf("backup=%v, want %v; conflict=%v", saved, tc.backup, err)
				}
			})
		}
		t.Run(p+"/type-change", func(t *testing.T) {
			excluded, err := CompareConflicts(Manifest{p: {}}, Manifest{p: {Dir: true}})
			if err == nil || len(excluded) != 0 {
				t.Fatalf("type change skipped: %v, %v", excluded, err)
			}
		})
	}
}

func TestExclusionRulesAreGeneric(t *testing.T) {
	rules := exclusionRules{Files: []string{"Custom/nested/metadata"}, Dirs: []string{"Generated"}}
	local := Manifest{"Custom": {Dir: true}, "Custom/nested": {Dir: true}, "Custom/nested/metadata": {}}
	for p, entry := range local {
		if !rules.matches(p, entry, local) {
			t.Fatalf("not excluded: %s", p)
		}
		if (exclusionRules{}).matches(p, entry, local) {
			t.Fatalf("removed rule still excluded: %s", p)
		}
	}
	local["Custom/empty"] = Entry{Dir: true}
	if rules.matches("Custom", local["Custom"], local) {
		t.Fatal("unlisted empty directory skipped")
	}
	delete(local, "Custom/empty")
	local["Custom/content.rtf"] = Entry{}
	if rules.matches("Custom", local["Custom"], local) {
		t.Fatal("content sibling skipped")
	}
	for _, p := range []string{"Generated/file", "Generated/nested/file"} {
		if !rules.matches(p, Entry{}, nil) {
			t.Fatalf("directory rule not applied: %s", p)
		}
	}
	for _, p := range []string{"Generated", "GeneratedElse/file", "Other/Generated/file", "custom/nested/metadata"} {
		if rules.matches(p, Entry{}, nil) {
			t.Fatalf("overbroad rule: %s", p)
		}
	}
}

func TestStatisticsAndContentRemainBlocking(t *testing.T) {
	for _, p := range []string{"Files/writing.history", "Files/Data/id/content.styles", "Files/styles.xml",
		"Settings/compile.xml", "Settings/projectpreferences.xml", "Project.scrivx", "Files/user.lock"} {
		if err := CheckConflicts(Manifest{p: {}}, Manifest{}); err == nil {
			t.Fatalf("unexpected exclusion: %s", p)
		}
	}
}
