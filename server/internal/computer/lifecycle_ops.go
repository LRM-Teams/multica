package computer

import (
	"context"
	"fmt"
)

// Health is the replaceable local liveness/readiness seam. Callers use this
// instead of knowing the Computer's singleton port or probing it directly.
func (l *Lifecycle) Health(ctx context.Context) map[string]any {
	v := l.view()
	return v.probe(ctx, v.service)
}

type RestartResult struct {
	Stop  StopResult
	Start StartResult
}

// Restart preserves the one-resident invariant by completing the stop before
// allocating and launching a fresh generation.
func (l *Lifecycle) Restart(options StartOptions) (RestartResult, error) {
	result := RestartResult{Stop: l.stop("restart")}
	if result.Stop.Err != nil {
		return result, result.Stop.Err
	}
	if result.Stop.Running && !result.Stop.Stopped {
		return result, fmt.Errorf("Computer did not stop; refusing to start a successor")
	}
	started, err := l.StartBackground(options)
	result.Start = started
	return result, err
}
