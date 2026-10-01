package archive

import (
	"path"
	"slices"
	"strings"
)

// Paths are exact, case-sensitive project-relative paths using forward slashes.
// Dirs include their entire subtree. Only conflict blocking is excluded:
// archives, integrity checks and reports still include these files.
var pullExclusions = exclusionRules{
	Files: []string{
		"Settings/ui.ini",
		"Settings/ui.plist",
		"Settings/ui-common.xml",
		"Settings/recents.txt",
		"Settings/favorites.xml",
		"Settings/templateinfo.xml",
		"Files/search.indexes",
		"Files/binder.autosave",
		"Files/binder.backup",
		"Files/Data/docs.checksum",
	},
	Dirs: []string{
		"QuickLook",
	},
}

type exclusionRules struct {
	Files []string
	Dirs  []string
}

func (rules exclusionRules) matches(p string, entry Entry, local Manifest) bool {
	for _, dir := range rules.Dirs {
		if strings.HasPrefix(p, dir+"/") || (entry.Dir && p == dir) {
			return true
		}
	}
	if !entry.Dir {
		return slices.Contains(rules.Files, p)
	}

	// A parent containing only excluded descendants should not block pull.
	// Empty directories outside Dirs still follow the normal conflict rules.
	found := false
	for child, childEntry := range local {
		if path.Dir(child) != p {
			continue
		}
		if !rules.matches(child, childEntry, local) {
			return false
		}
		found = true
	}
	return found
}
