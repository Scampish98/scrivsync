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

func TestParseCommand(t *testing.T) {
	for _, tc := range []struct {
		args    []string
		command string
		force   bool
	}{
		{[]string{"push"}, "push", false},
		{[]string{"pull"}, "pull", false},
		{[]string{"pull", "--force"}, "pull", true},
	} {
		command, force, err := parseCommand(tc.args)
		if err != nil || command != tc.command || force != tc.force {
			t.Fatalf("%v: %q, %v, %v", tc.args, command, force, err)
		}
	}
	for _, args := range [][]string{{"push", "--force"}, {"pull", "--unknown"}, {"pull", "--force", "extra"}, {"--force", "pull"}} {
		if _, _, err := parseCommand(args); err == nil {
			t.Fatalf("accepted invalid arguments: %v", args)
		}
	}
}
