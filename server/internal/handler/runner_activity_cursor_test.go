package handler

import "testing"

func TestRunnerActivityCursorAcceptsReplayAndRejectsRegression(t *testing.T) {
	notes := newRunnerActivityCursorStore()
	key := runnerActivityCursorKey{
		workspaceID: "ws", agentID: "agent", daemonID: "daemon",
		daemonInstanceID: "instance",
	}
	if !notes.accept(key, 2, "daemon_activity:agent:launch:instance:2") {
		t.Fatal("first fact rejected")
	}
	if !notes.accept(key, 2, "daemon_activity:agent:launch:instance:2") {
		t.Fatal("identical replay rejected")
	}
	if notes.accept(key, 2, "other-fact") {
		t.Fatal("same seq with a different fact accepted")
	}
	if notes.accept(key, 1, "daemon_activity:agent:launch:instance:1") {
		t.Fatal("older seq accepted")
	}
	if !notes.accept(key, 3, "daemon_activity:agent:launch:instance:3") {
		t.Fatal("newer seq rejected")
	}
}

func TestRunnerActivityCursorDiesWithInstance(t *testing.T) {
	notes := newRunnerActivityCursorStore()
	oldKey := runnerActivityCursorKey{
		workspaceID: "ws", agentID: "agent", daemonID: "daemon",
		daemonInstanceID: "old",
	}
	newKey := oldKey
	newKey.daemonInstanceID = "new"
	if !notes.accept(oldKey, 9, "old-9") {
		t.Fatal("seed rejected")
	}
	notes.forgetOtherInstances("ws", "daemon", "new")
	if !notes.accept(newKey, 1, "new-1") {
		t.Fatal("replacement instance inherited the old seq note")
	}
	notes.forgetInstance("ws", "daemon", "new")
	if !notes.accept(newKey, 1, "new-1-again") {
		t.Fatal("disconnect left the instance note in place")
	}
}
