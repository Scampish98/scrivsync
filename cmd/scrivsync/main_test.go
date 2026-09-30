package main

import (
	"context"
	"io"
	"strings"
	"testing"
)

func TestCommandValidation(t *testing.T) {
	if err := execute(context.Background(), []string{"--help"}, io.Discard); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{nil, {"wrong"}, {"push", "x", "disk:/a.zip"}} {
		if err := execute(context.Background(), args, io.Discard); err == nil || !strings.Contains(err.Error(), "config.yaml") {
			t.Fatalf("invalid command accepted: %v", err)
		}
	}
}
