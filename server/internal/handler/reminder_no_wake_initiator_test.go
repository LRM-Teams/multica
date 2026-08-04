package handler

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// Product lock (Frank/Parker 2026-08-04): reminder transport must not resolve
// initiator via wake (agentTransportInitiatorUserID / task / inbox wake helpers).
// Schedule uses agentReminderScheduleInitiatorUserID (anchor→owner→ws owner);
// cancel/update/snooze are owning-agent only (authorizeNaturalLanguageReminderMutation).
func TestReminderSourceHasNoWakeInitiatorDependency(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	dir := filepath.Dir(thisFile)
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	forbidden := []string{
		"agentTransportInitiatorUserID",
		"agentTaskInitiatorUserID",
		"agentInboxInitiatorUserID",
	}
	for _, ent := range entries {
		name := ent.Name()
		if ent.IsDir() || !strings.HasPrefix(name, "reminder") || !strings.HasSuffix(name, ".go") {
			continue
		}
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		body, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		src := string(body)
		for _, needle := range forbidden {
			if strings.Contains(src, needle) {
				t.Errorf("%s must not call/reference %s (wake initiator path forbidden on reminder)", name, needle)
			}
		}
	}
}
