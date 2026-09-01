package handler

import (
	"testing"
	"time"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestRunnerStartRetryDecisionStopsTerminalFailureLoop(t *testing.T) {
	decision := decideRunnerStartRetry(&protocol.AgentStartFailure{
		ReasonCode: "provider_auth_required", RetryClass: protocol.AgentStartRetryTerminal,
	}, 1)
	if decision.retry || decision.delay != 0 {
		t.Fatalf("terminal decision=%+v, want no automatic retry", decision)
	}
}

func TestRunnerStartRetryDecisionBacksOffTransientFailure(t *testing.T) {
	want := []time.Duration{5 * time.Second, 15 * time.Second, time.Minute, time.Minute}
	for index, delay := range want {
		decision := decideRunnerStartRetry(&protocol.AgentStartFailure{
			ReasonCode: "provider_unavailable", RetryClass: protocol.AgentStartRetryTransient,
		}, index+1)
		if !decision.retry || decision.delay != delay {
			t.Fatalf("attempt %d decision=%+v, want retry after %s", index+1, decision, delay)
		}
	}
}

func TestRunnerStartRetryJitterStaysWithinTwentyPercent(t *testing.T) {
	const base = time.Minute
	for index := 0; index < 100; index++ {
		got := jitterRunnerStartRetry(base)
		if got < 48*time.Second || got > 72*time.Second {
			t.Fatalf("jittered retry = %s, want within 20%% of %s", got, base)
		}
	}
}
