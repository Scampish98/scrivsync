package archive

import "testing"

func TestInterfaceAndStyleConflicts(t *testing.T) {
	for _, tc := range []struct {
		name            string
		local, incoming Manifest
		conflict        bool
	}{
		{"ui changed", Manifest{"Settings/ui.ini": {Hash: "local"}}, Manifest{"Settings/ui.ini": {Hash: "remote"}}, false},
		{"ui only local", Manifest{"Settings": {Dir: true}, "Settings/ui.ini": {Hash: "local"}}, Manifest{}, false},
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
