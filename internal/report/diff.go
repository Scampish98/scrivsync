package report

import (
	"bytes"
	"context"
	"fmt"
	"strings"
)

const (
	contextLines = 3
	maxDiffCells = 1_000_000
)

type lineChange struct {
	kind byte
	text string
}

// unifiedDiff compares the original lines, including their line endings.
// Large comparisons fall back to replacing the changed block to bound memory.
func unifiedDiff(ctx context.Context, before, after []byte, beforeName, afterName string) (string, error) {
	oldLines, newLines := splitLines(before), splitLines(after)
	changes, err := compareLines(ctx, oldLines, newLines)
	if err != nil {
		return "", err
	}

	var out bytes.Buffer
	fmt.Fprintf(&out, "--- %s\n+++ %s\n", beforeName, afterName)
	oldLine, newLine := 1, 1
	for i := 0; i < len(changes); {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		if changes[i].kind == ' ' {
			oldLine++
			newLine++
			i++
			continue
		}

		start := max(0, i-contextLines)
		end := hunkEnd(changes, i)
		oldStart, newStart := oldLine-(i-start), newLine-(i-start)
		oldCount, newCount := 0, 0
		for _, change := range changes[start:end] {
			if change.kind != '+' {
				oldCount++
			}
			if change.kind != '-' {
				newCount++
			}
		}
		if oldCount == 0 {
			oldStart--
		}
		if newCount == 0 {
			newStart--
		}
		fmt.Fprintf(&out, "@@ -%d,%d +%d,%d @@\n", oldStart, oldCount, newStart, newCount)
		for _, change := range changes[start:end] {
			out.WriteByte(change.kind)
			out.WriteString(change.text)
			if !strings.HasSuffix(change.text, "\n") {
				out.WriteString("\n\\ No newline at end of file\n")
			}
		}

		for _, change := range changes[i:end] {
			if change.kind != '+' {
				oldLine++
			}
			if change.kind != '-' {
				newLine++
			}
		}
		i = end
	}

	return out.String(), nil
}

func splitLines(data []byte) []string {
	if len(data) == 0 {
		return nil
	}
	lines := strings.SplitAfter(string(data), "\n")
	if lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

func hunkEnd(changes []lineChange, first int) int {
	last := first
	for i := first + 1; i < len(changes); i++ {
		if changes[i].kind != ' ' {
			if i-last > 2*contextLines+1 {
				break
			}
			last = i
		}
	}
	return min(len(changes), last+contextLines+1)
}

func compareLines(ctx context.Context, oldLines, newLines []string) ([]lineChange, error) {
	var result []lineChange
	for len(oldLines) > 0 && len(newLines) > 0 && oldLines[0] == newLines[0] {
		result = append(result, lineChange{' ', oldLines[0]})
		oldLines, newLines = oldLines[1:], newLines[1:]
	}

	suffix := 0
	for suffix < len(oldLines) && suffix < len(newLines) && oldLines[len(oldLines)-1-suffix] == newLines[len(newLines)-1-suffix] {
		suffix++
	}
	tail := oldLines[len(oldLines)-suffix:]
	oldLines, newLines = oldLines[:len(oldLines)-suffix], newLines[:len(newLines)-suffix]

	middle, err := compareBlock(ctx, oldLines, newLines)
	if err != nil {
		return nil, err
	}
	result = append(result, middle...)
	for _, line := range tail {
		result = append(result, lineChange{' ', line})
	}
	return result, nil
}

func compareBlock(ctx context.Context, oldLines, newLines []string) ([]lineChange, error) {
	n, m := len(oldLines), len(newLines)
	var lengths []uint32
	if n+1 <= maxDiffCells/(m+1) {
		lengths = make([]uint32, (n+1)*(m+1))
		for i := n - 1; i >= 0; i-- {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			for j := m - 1; j >= 0; j-- {
				index := i*(m+1) + j
				if oldLines[i] == newLines[j] {
					lengths[index] = 1 + lengths[(i+1)*(m+1)+j+1]
				} else {
					lengths[index] = max(lengths[(i+1)*(m+1)+j], lengths[index+1])
				}
			}
		}
	}

	result := make([]lineChange, 0, n+m)
	for i, j := 0, 0; i < n || j < m; {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		switch {
		case i < n && j < m && oldLines[i] == newLines[j]:
			result = append(result, lineChange{' ', oldLines[i]})
			i++
			j++
		case i < n && (j == m || lengths == nil || lengths[(i+1)*(m+1)+j] >= lengths[i*(m+1)+j+1]):
			result = append(result, lineChange{'-', oldLines[i]})
			i++
		default:
			result = append(result, lineChange{'+', newLines[j]})
			j++
		}
	}
	return result, nil
}
