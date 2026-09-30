package archive

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

type ConflictError struct{ Items []string }

func (e *ConflictError) Error() string {
	return "pull отменён: конфликты\n  " + strings.Join(e.Items, "\n  ")
}

func CheckConflicts(local, incoming Manifest) error {
	var issues []string
	for p, l := range local {
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
