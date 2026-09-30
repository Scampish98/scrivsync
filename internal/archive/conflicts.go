package archive

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

type ConflictError struct{ Items []string }

func (e *ConflictError) Error() string {
	return "конфликты pull\n  " + strings.Join(e.Items, "\n  ")
}

func CheckConflicts(local, incoming Manifest) error {
	var issues []string
	for p, l := range local {
		if interfaceOnlyDifference(p, l, local, incoming) {
			continue
		}
		r, ok := incoming[p]
		if !ok {
			issues = append(issues, fmt.Sprintf("%q: существует только локально", p))
			continue
		}
		if l.Dir != r.Dir {
			issues = append(issues, fmt.Sprintf("%q: файл и папка имеют одинаковый путь", p))
			continue
		}
		if l.Dir || l.Hash == r.Hash {
			continue
		}
		if r.Modified.Sub(l.Modified) <= 2*time.Second {
			reason := "различное содержимое; даты отличаются не более чем на 2 секунды"
			if l.Modified.Sub(r.Modified) > 2*time.Second {
				reason = "локальный файл новее"
			}
			issues = append(issues, fmt.Sprintf("%q: %s (локально %s; архив %s)", p, reason, l.Modified.Format(time.RFC3339Nano), r.Modified.Format(time.RFC3339Nano)))
		}
	}
	if len(issues) == 0 {
		return nil
	}
	sort.Strings(issues)
	return &ConflictError{Items: issues}
}

// UI preferences travel with the project, but do not block accepting its contents.
// A type change is still a structural conflict.
func interfaceOnlyDifference(p string, entry Entry, local, incoming Manifest) bool {
	remote, exists := incoming[p]
	if p == "Settings/ui.ini" {
		return !entry.Dir && (!exists || !remote.Dir)
	}
	if p != "Settings" || !entry.Dir || exists {
		return false
	}

	// Do not report the parent of a local-only ui.ini as a separate conflict.
	found := false
	for child, childEntry := range local {
		if !strings.HasPrefix(child, "Settings/") {
			continue
		}
		if child != "Settings/ui.ini" || childEntry.Dir {
			return false
		}
		found = true
	}
	return found
}
