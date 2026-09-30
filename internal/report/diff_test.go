package report

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestUnifiedDiff(t *testing.T) {
	for _, tc := range []struct{ name, before, after, want string }{
		{"replace", "один\nстарый\nтри\n", "один\nновый\nтри\n", "@@ -1,3 +1,3 @@\n один\n-старый\n+новый\n три\n"},
		{"add", "", "новый\n", "@@ -0,0 +1,1 @@\n+новый\n"},
		{"remove", "старый\n", "", "@@ -1,1 +0,0 @@\n-старый\n"},
		{"newline", "text", "text\n", "@@ -1,1 +1,1 @@\n-text\n\\ No newline at end of file\n+text\n"},
		{"crlf", "text\r\n", "text\n", "@@ -1,1 +1,1 @@\n-text\r\n+text\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := unifiedDiff(context.Background(), []byte(tc.before), []byte(tc.after), "local", "archive")
			if err != nil || got != "--- local\n+++ archive\n"+tc.want {
				t.Fatalf("diff = %q, %v; want %q", got, err, tc.want)
			}
		})
	}
}

func TestUnifiedDiffSeparateHunks(t *testing.T) {
	before := "old\n" + strings.Repeat("same\n", 12) + "last\n"
	after := "new\nextra\n" + strings.Repeat("same\n", 12) + "changed\n"
	got, err := unifiedDiff(context.Background(), []byte(before), []byte(after), "local", "archive")
	if err != nil {
		t.Fatal(err)
	}
	want := "--- local\n+++ archive\n@@ -1,4 +1,5 @@\n-old\n+new\n+extra\n same\n same\n same\n@@ -11,4 +12,4 @@\n same\n same\n same\n-last\n+changed\n"
	if got != want {
		t.Fatalf("got %q; want %q", got, want)
	}
}

func TestLargeDiffFallbackPreservesBothVersions(t *testing.T) {
	before := strings.Repeat("old\n", 1100)
	after := strings.Repeat("new\n", 1100)
	changes, err := compareLines(context.Background(), splitLines([]byte(before)), splitLines([]byte(after)))
	if err != nil {
		t.Fatal(err)
	}
	var oldText, newText strings.Builder
	for _, change := range changes {
		if change.kind != '+' {
			oldText.WriteString(change.text)
		}
		if change.kind != '-' {
			newText.WriteString(change.text)
		}
	}
	if oldText.String() != before || newText.String() != after {
		t.Fatal("fallback lost file contents")
	}
}

func TestDiffCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := unifiedDiff(ctx, []byte("old"), []byte("new"), "local", "archive"); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected cancellation, got %v", err)
	}
}
