package archive

import "testing"

func TestGeneratedFilesAndStyleConflicts(t *testing.T) {
	for _, tc := range []struct {
		name            string
		local, incoming Manifest
		conflict        bool
	}{
		{"ui changed", Manifest{"Settings/ui.ini": {Hash: "local"}}, Manifest{"Settings/ui.ini": {Hash: "remote"}}, false},
		{"ui only local", Manifest{"Settings": {Dir: true}, "Settings/ui.ini": {Hash: "local"}}, Manifest{}, false},
		{"mac ui changed", Manifest{"Settings/ui.plist": {Hash: "local"}}, Manifest{"Settings/ui.plist": {Hash: "remote"}}, false},
		{"both ui files only local", Manifest{"Settings": {Dir: true}, "Settings/ui.ini": {}, "Settings/ui.plist": {}}, Manifest{}, false},
		{"mac ui type changed", Manifest{"Settings/ui.plist": {}}, Manifest{"Settings/ui.plist": {Dir: true}}, true},
		{"mac ui local directory", Manifest{"Settings/ui.plist": {Dir: true}}, Manifest{}, true},
		{"preview changed", Manifest{"QuickLook/Preview.html": {Hash: "local"}}, Manifest{"QuickLook/Preview.html": {Hash: "remote"}}, false},
		{"preview only local", Manifest{"QuickLook": {Dir: true}, "QuickLook/Preview.html": {}, "QuickLook/Thumbnail.jpg": {}}, Manifest{}, false},
		{"nested preview cache", Manifest{"QuickLook": {Dir: true}, "QuickLook/cache": {Dir: true}, "QuickLook/cache/preview": {}}, Manifest{}, false},
		{"preview type changed", Manifest{"QuickLook/Preview.html": {}}, Manifest{"QuickLook/Preview.html": {Dir: true}}, true},
		{"preview root type changed", Manifest{"QuickLook": {Dir: true}}, Manifest{"QuickLook": {}}, true},
		{"preview root is file", Manifest{"QuickLook": {}}, Manifest{}, true},
		{"unrelated preview", Manifest{"Files/QuickLook/Preview.html": {}}, Manifest{}, true},
		{"other settings", Manifest{"Settings": {Dir: true}, "Settings/ui.ini": {}, "Settings/other": {}}, Manifest{}, true},
		{"ui type changed", Manifest{"Settings/ui.ini": {}}, Manifest{"Settings/ui.ini": {Dir: true}}, true},
		{"ui local directory", Manifest{"Settings/ui.ini": {Dir: true}}, Manifest{}, true},
		{"unrelated ui", Manifest{"Files/ui.ini": {}}, Manifest{}, true},
		{"styles only local", Manifest{"Files/Data/id/content.styles": {Hash: "local"}}, Manifest{}, true},
		{"styles changed", Manifest{"Files/Data/id/notes.styles": {Hash: "local"}}, Manifest{"Files/Data/id/notes.styles": {Hash: "remote"}}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := CheckConflicts(tc.local, tc.incoming); (err != nil) != tc.conflict {
				t.Fatalf("unexpected conflict result: %v", err)
			}
		})
	}
}
