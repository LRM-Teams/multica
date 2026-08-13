package daemon

import (
	"errors"
	"sync"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

const agentLifecycleReceiptCacheSize = 1024

type agentLifecycleCommandIdentity struct {
	workspaceID string
	agentID     string
	runtimeID   string
	actionKind  string
}

type agentLifecycleCommandReceipt struct {
	identity agentLifecycleCommandIdentity
	result   protocol.WorkspaceRunnerAgentLifecycleResultPayload
}

// agentLifecycleReceiptCache mirrors Raft's accepted/accepting dispatch maps:
// one process executes a command once and replays its receipt to local relay
// duplicates. It is deliberately neither durable nor a delivery scheduler.
type agentLifecycleReceiptCache struct {
	mu        sync.Mutex
	inFlight  map[string]agentLifecycleCommandIdentity
	completed map[string]agentLifecycleCommandReceipt
	order     []string
}

func newAgentLifecycleReceiptCache() *agentLifecycleReceiptCache {
	return &agentLifecycleReceiptCache{
		inFlight:  make(map[string]agentLifecycleCommandIdentity),
		completed: make(map[string]agentLifecycleCommandReceipt),
	}
}

func (cache *agentLifecycleReceiptCache) Accept(command protocol.WorkspaceRunnerAgentLifecyclePayload) (protocol.WorkspaceRunnerAgentLifecycleAckPayload, *protocol.WorkspaceRunnerAgentLifecycleResultPayload, bool, error) {
	if cache == nil {
		return protocol.WorkspaceRunnerAgentLifecycleAckPayload{}, nil, false, errors.New("Agent lifecycle receipt cache is unavailable")
	}
	identity := agentLifecycleCommandIdentity{
		workspaceID: command.WorkspaceID,
		agentID:     command.AgentID,
		runtimeID:   command.RuntimeID,
		actionKind:  command.ActionKind,
	}
	ack := protocol.WorkspaceRunnerAgentLifecycleAckPayload{
		OperationID: command.OperationID,
		AgentID:     command.AgentID,
		RuntimeID:   command.RuntimeID,
		Outcome:     protocol.AgentLifecycleCommandAccepted,
	}
	cache.mu.Lock()
	defer cache.mu.Unlock()
	if accepted, ok := cache.inFlight[command.OperationID]; ok {
		if accepted != identity {
			return protocol.WorkspaceRunnerAgentLifecycleAckPayload{}, nil, false, errors.New("Agent lifecycle operation identity conflicts with its accepted receipt")
		}
		ack.Outcome = protocol.AgentLifecycleCommandDuplicate
		return ack, nil, false, nil
	}
	if receipt, ok := cache.completed[command.OperationID]; ok {
		if receipt.identity != identity {
			return protocol.WorkspaceRunnerAgentLifecycleAckPayload{}, nil, false, errors.New("Agent lifecycle operation identity conflicts with its terminal receipt")
		}
		ack.Outcome = protocol.AgentLifecycleCommandDuplicate
		result := receipt.result
		return ack, &result, false, nil
	}
	cache.inFlight[command.OperationID] = identity
	return ack, nil, true, nil
}

func (cache *agentLifecycleReceiptCache) Complete(command protocol.WorkspaceRunnerAgentLifecyclePayload, result protocol.WorkspaceRunnerAgentLifecycleResultPayload) {
	if cache == nil {
		return
	}
	cache.mu.Lock()
	defer cache.mu.Unlock()
	identity, ok := cache.inFlight[command.OperationID]
	if !ok {
		return
	}
	delete(cache.inFlight, command.OperationID)
	cache.completed[command.OperationID] = agentLifecycleCommandReceipt{identity: identity, result: result}
	cache.order = append(cache.order, command.OperationID)
	for len(cache.order) > agentLifecycleReceiptCacheSize {
		oldest := cache.order[0]
		cache.order = cache.order[1:]
		delete(cache.completed, oldest)
	}
}
