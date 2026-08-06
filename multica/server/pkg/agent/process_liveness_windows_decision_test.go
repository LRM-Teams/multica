package agent

import "testing"

func TestWindowsProcessLivenessDecision(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		openResult    windowsProcessOpenResult
		exitCode      uint32
		exitCodeKnown bool
		wantAlive     bool
		wantKnown     bool
	}{
		{name: "access denied proves alive", openResult: windowsProcessOpenAccessDenied, wantAlive: true, wantKnown: true},
		{name: "not found proves dead", openResult: windowsProcessOpenNotFound, wantKnown: true},
		{name: "other open error is unknown", openResult: windowsProcessOpenUnknown},
		{name: "get exit code error is unknown", openResult: windowsProcessOpenSucceeded},
		{name: "still active", openResult: windowsProcessOpenSucceeded, exitCode: windowsProcessStillActive, exitCodeKnown: true, wantAlive: true, wantKnown: true},
		{name: "exited", openResult: windowsProcessOpenSucceeded, exitCode: 0, exitCodeKnown: true, wantKnown: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotAlive, gotKnown := windowsProcessLivenessDecision(tt.openResult, tt.exitCode, tt.exitCodeKnown)
			if gotAlive != tt.wantAlive || gotKnown != tt.wantKnown {
				t.Fatalf("decision = (alive=%v, known=%v), want (alive=%v, known=%v)", gotAlive, gotKnown, tt.wantAlive, tt.wantKnown)
			}
		})
	}
}
