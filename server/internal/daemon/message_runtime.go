package daemon

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"sort"
	"strconv"
	"strings"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

func (d *Daemon) ensureIdleMessageCoordinator(agentID, runtimeID, agentRoot string) (bool, error) {
	if d == nil || strings.TrimSpace(agentID) == "" || strings.TrimSpace(runtimeID) == "" {
		return false, errors.New("agent and runtime ids are required")
	}
	d.messageCoordinatorMu.Lock()
	defer d.messageCoordinatorMu.Unlock()
	if existing := d.messageCoordinators[agentID]; existing != nil {
		if d.messageRuntimeIDs[agentID] == "" || d.messageRuntimeIDs[agentID] == runtimeID {
			return false, nil
		}
		existing.Close()
	}
	coordinator, err := NewMessageCoordinator(agentRoot, func(ctx context.Context, messages []protocol.AgentMessageProjection) error {
		return d.canonicalRuntimes.handoffIdleMessages(ctx, agentID, runtimeID, messages)
	}, func(messages []protocol.AgentMessageProjection) {
		d.emitMessageReceivedActivity(agentID, runtimeID, messages)
	})
	if err != nil {
		return false, err
	}
	coordinator.ConfigurePendingNotices(func(ctx context.Context, snapshot PendingNoticeSnapshot) error {
		return d.canonicalRuntimes.handoffBusyNotice(ctx, agentID, runtimeID, snapshot)
	}, 0, 0)
	if d.messageCoordinators == nil {
		d.messageCoordinators = make(map[string]*MessageCoordinator)
	}
	if d.messageRuntimeIDs == nil {
		d.messageRuntimeIDs = make(map[string]string)
	}
	d.messageCoordinators[agentID] = coordinator
	d.messageRuntimeIDs[agentID] = runtimeID
	return true, nil
}

func (d *Daemon) emitMessageReceivedActivity(agentID, runtimeID string, messages []protocol.AgentMessageProjection) {
	if len(messages) == 0 {
		return
	}
	targetSet := make(map[string]struct{})
	identity := make([]string, 0, len(messages))
	for _, message := range messages {
		targetSet[message.Target] = struct{}{}
		identity = append(identity, message.ID+"\x00"+message.Target+"\x00"+strconv.FormatInt(message.Seq, 10))
	}
	sort.Strings(identity)
	sum := sha256.Sum256([]byte(strings.Join(identity, "\x01")))
	targets := make([]string, 0, len(targetSet))
	for target := range targetSet {
		targets = append(targets, target)
	}
	sort.Strings(targets)
	d.queueReminderFrame(protocol.EventAgentMessageHandoff, protocol.AgentMessageHandoffPayload{
		AgentID: agentID, RuntimeID: runtimeID, HandoffID: hex.EncodeToString(sum[:]), Count: len(messages), Targets: targets,
	})
}

// CredentialProxy is the machine-local freshness boundary. The first repair
// slice exposes only the internally derived target sequence; it deliberately
// does not accept an Agent-supplied cursor.
type CredentialProxy struct{ daemon *Daemon }

func (d *Daemon) CredentialProxy() *CredentialProxy { return &CredentialProxy{daemon: d} }

func (p *CredentialProxy) SeenUpToSeq(agentID, target string) (int64, error) {
	if p == nil || p.daemon == nil {
		return 0, errors.New("Credential Proxy is unavailable")
	}
	p.daemon.messageCoordinatorMu.RLock()
	defer p.daemon.messageCoordinatorMu.RUnlock()
	coordinator := p.daemon.messageCoordinators[agentID]
	if coordinator == nil {
		return 0, errors.New("Message coordinator is unavailable")
	}
	seq, known := coordinator.ContextBoundary(target)
	if !known {
		return 0, errors.New("Message freshness is unknown")
	}
	return seq, nil
}

func (p *CredentialProxy) CheckMessages(agentID string) (MessageCheckResult, error) {
	if p == nil || p.daemon == nil {
		return MessageCheckResult{}, errors.New("Credential Proxy is unavailable")
	}
	p.daemon.messageCoordinatorMu.RLock()
	defer p.daemon.messageCoordinatorMu.RUnlock()
	coordinator := p.daemon.messageCoordinators[strings.TrimSpace(agentID)]
	if coordinator == nil {
		return MessageCheckResult{}, errors.New("Message coordinator is unavailable")
	}
	return coordinator.Check(messageCheckDefaultLimit)
}
