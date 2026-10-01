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
	_, err := CompareConflicts(local, incoming)
	return err
}

type ConflictOverride struct {
	Accept bool
	Reason string
}

// CompareConflicts returns excluded local files that need a backup, separately
// from conflicts that block installation. Both use the same conflict rules.
func CompareConflicts(local, incoming Manifest) (Manifest, error) {
	return CompareConflictsWithOverrides(local, incoming, nil)
}

// Overrides apply only to two existing ordinary files, never to type changes.
func CompareConflictsWithOverrides(local, incoming Manifest, overrides map[string]ConflictOverride) (Manifest, error) {
	excluded := Manifest{}
	var issues []string
	for p, l := range local {
		r, exists := incoming[p]
		reason := conflictReason(l, r, exists)
		if decision, ok := overrides[p]; ok && exists && !l.Dir && !r.Dir && l.Hash != r.Hash {
			if decision.Accept {
				if reason != "" {
					excluded[p] = l
				}
				continue
			}
			reason = decision.Reason
		}
		if reason == "" {
			continue
		}

		// A type change always blocks, even when the path is excluded.
		if (!exists || l.Dir == r.Dir) && pullExclusions.matches(p, l, local) {
			if !l.Dir {
				excluded[p] = l
			}
			continue
		}
		issues = append(issues, fmt.Sprintf("%q: %s", p, reason))
	}
	if len(issues) == 0 {
		return excluded, nil
	}

	sort.Strings(issues)
	return excluded, &ConflictError{Items: issues}
}

func conflictReason(local, remote Entry, exists bool) string {
	if !exists {
		return "существует только локально"
	}
	if local.Dir != remote.Dir {
		return "файл и папка имеют одинаковый путь"
	}
	if local.Dir || local.Hash == remote.Hash || remote.Modified.Sub(local.Modified) > 2*time.Second {
		return ""
	}

	reason := "различное содержимое; даты отличаются не более чем на 2 секунды"
	if local.Modified.Sub(remote.Modified) > 2*time.Second {
		reason = "локальный файл новее"
	}
	return fmt.Sprintf("%s (локально %s; архив %s)", reason,
		local.Modified.Format(time.RFC3339Nano), remote.Modified.Format(time.RFC3339Nano))
}
