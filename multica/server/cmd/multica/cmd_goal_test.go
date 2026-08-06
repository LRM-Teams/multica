package main

import (
	"testing"

	"github.com/spf13/cobra"
)

func TestGoalCommandsAreRegisteredWithRequiredSafetyFlags(t *testing.T) {
	for _, tc := range []struct {
		name  string
		flags []string
	}{
		{name: "get", flags: []string{"channel"}},
		{name: "create", flags: []string{"channel", "title", "objective", "criterion"}},
		{name: "checkpoint", flags: []string{"channel", "expected-version", "progress", "current-step", "blocker", "evidence", "completed-criterion"}},
		{name: "update", flags: []string{"channel", "expected-version", "title", "objective", "criterion", "status"}},
	} {
		command, _, err := goalCmd.Find([]string{tc.name})
		if err != nil || command == nil || command.Name() != tc.name {
			t.Fatalf("goal %s not registered: command=%#v err=%v", tc.name, command, err)
		}
		for _, flag := range tc.flags {
			if command.Flags().Lookup(flag) == nil {
				t.Errorf("goal %s missing --%s", tc.name, flag)
			}
		}
	}
	for _, tc := range []struct {
		path  []string
		flags []string
	}{
		{path: []string{"process", "list"}, flags: []string{"channel"}},
		{path: []string{"process", "get"}, flags: []string{"channel", "agent"}},
		{path: []string{"process", "put"}, flags: []string{"channel", "agent", "expected-version", "content", "content-file"}},
	} {
		command, _, err := goalCmd.Find(tc.path)
		if err != nil || command == nil {
			t.Fatalf("goal %v not registered: %v", tc.path, err)
		}
		for _, flag := range tc.flags {
			if command.Flags().Lookup(flag) == nil {
				t.Errorf("goal %v missing --%s", tc.path, flag)
			}
		}
	}
}

func TestGoalUpdateRejectsEmptyMutation(t *testing.T) {
	cmd := &cobra.Command{}
	cmd.Flags().Int64("expected-version", 3, "")
	cmd.Flags().String("title", "", "")
	cmd.Flags().String("objective", "", "")
	cmd.Flags().StringSlice("criterion", nil, "")
	cmd.Flags().String("status", "", "")
	if _, err := goalUpdateBody(cmd); err == nil {
		t.Fatal("empty goal update unexpectedly accepted")
	}
	if err := cmd.Flags().Set("status", "paused"); err != nil {
		t.Fatal(err)
	}
	body, err := goalUpdateBody(cmd)
	if err != nil {
		t.Fatalf("status update: %v", err)
	}
	if body["expected_version"] != int64(3) || body["status"] != "paused" || len(body) != 2 {
		t.Fatalf("status update body = %#v", body)
	}
}
