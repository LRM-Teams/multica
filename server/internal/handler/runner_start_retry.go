package handler

import (
	"context"
	"log/slog"
	"math/rand"
	"sync"
	"time"

	"github.com/multica-ai/multica/server/internal/daemonws"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

var runnerStartRetrySchedule = []time.Duration{5 * time.Second, 15 * time.Second, time.Minute}

type runnerStartRetryDecision struct {
	retry bool
	delay time.Duration
}

func decideRunnerStartRetry(failure *protocol.AgentStartFailure, attempt int) runnerStartRetryDecision {
	if failure == nil || failure.RetryClass != protocol.AgentStartRetryTransient || attempt <= 0 {
		return runnerStartRetryDecision{}
	}
	index := attempt - 1
	if index >= len(runnerStartRetrySchedule) {
		index = len(runnerStartRetrySchedule) - 1
	}
	return runnerStartRetryDecision{retry: true, delay: runnerStartRetrySchedule[index]}
}

func jitterRunnerStartRetry(delay time.Duration) time.Duration {
	if delay <= 0 {
		return delay
	}
	spread := delay / 5
	return delay + time.Duration(rand.Int63n(int64(spread)*2+1)) - spread
}

type runnerStartRetryKey struct {
	workspaceID string
	agentID     string
}

type runnerStartRetryEntry struct {
	attempt int
	timer   *time.Timer
}

type runnerStartRetryScheduler struct {
	mu      sync.Mutex
	entries map[runnerStartRetryKey]runnerStartRetryEntry
}

func newRunnerStartRetryScheduler() *runnerStartRetryScheduler {
	return &runnerStartRetryScheduler{entries: make(map[runnerStartRetryKey]runnerStartRetryEntry)}
}

func (scheduler *runnerStartRetryScheduler) clear(workspaceID, agentID string) {
	if scheduler == nil {
		return
	}
	key := runnerStartRetryKey{workspaceID: workspaceID, agentID: agentID}
	scheduler.mu.Lock()
	entry, ok := scheduler.entries[key]
	delete(scheduler.entries, key)
	scheduler.mu.Unlock()
	if ok && entry.timer != nil {
		entry.timer.Stop()
	}
}

func (scheduler *runnerStartRetryScheduler) schedule(workspaceID, agentID string, failure protocol.AgentStartFailure, retry func()) runnerStartRetryDecision {
	if scheduler == nil {
		return runnerStartRetryDecision{}
	}
	key := runnerStartRetryKey{workspaceID: workspaceID, agentID: agentID}
	scheduler.mu.Lock()
	entry := scheduler.entries[key]
	if entry.timer != nil {
		entry.timer.Stop()
	}
	entry.attempt++
	decision := decideRunnerStartRetry(&failure, entry.attempt)
	if !decision.retry {
		delete(scheduler.entries, key)
		scheduler.mu.Unlock()
		return decision
	}
	decision.delay = jitterRunnerStartRetry(decision.delay)
	attempt := entry.attempt
	entry.timer = time.AfterFunc(decision.delay, func() {
		scheduler.mu.Lock()
		current, ok := scheduler.entries[key]
		if !ok || current.attempt != attempt {
			scheduler.mu.Unlock()
			return
		}
		current.timer = nil
		scheduler.entries[key] = current
		scheduler.mu.Unlock()
		retry()
	})
	scheduler.entries[key] = entry
	scheduler.mu.Unlock()
	return decision
}

func (h *Handler) runnerStartRetries() *runnerStartRetryScheduler {
	if h.runnerStartRetry == nil {
		h.runnerStartRetry = newRunnerStartRetryScheduler()
	}
	return h.runnerStartRetry
}

func (h *Handler) scheduleRunnerStartRetry(identity daemonws.ClientIdentity, agentID string, failure protocol.AgentStartFailure) {
	decision := h.runnerStartRetries().schedule(identity.WorkspaceID, agentID, failure, func() {
		if err := h.reconcileDesiredAgentRuntime(context.Background(), identity.WorkspaceID, agentID); err != nil {
			slog.Warn("WorkspaceDaemon provider start retry failed", "workspace_id", identity.WorkspaceID, "daemon_id", identity.DaemonID, "agent_id", agentID, "error", err)
		}
	})
	if decision.retry {
		slog.Warn("WorkspaceDaemon provider start retry scheduled", "workspace_id", identity.WorkspaceID, "daemon_id", identity.DaemonID, "agent_id", agentID, "reason", failure.ReasonCode, "retry_in", decision.delay)
		return
	}
	slog.Warn("WorkspaceDaemon provider start auto-retry stopped", "workspace_id", identity.WorkspaceID, "daemon_id", identity.DaemonID, "agent_id", agentID, "reason", failure.ReasonCode)
}
