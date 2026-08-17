package researchrun

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
)

type V6OutboxIntent struct {
	ID, WorkspaceID, RunID, Kind, IdempotencyKey string
	Payload                                      json.RawMessage
	DeliveryAttempts                             int
}

type v6OutboxStore interface {
	ClaimV6Outbox(context.Context, string, time.Duration, int) ([]V6OutboxIntent, error)
	CompleteV6Outbox(context.Context, string, string, json.RawMessage) error
	RescheduleV6Outbox(context.Context, string, string, string, time.Time) error
	CompleteV6DispatchOutbox(context.Context, string, string, string) error
}

type v6RuntimeModule struct {
	store  v6OutboxStore
	team   teamV6Store
	agents AgentLifecycleAdapter
	inbox  InboxDispatchAdapter
	clock  Clock
}

func (m v6RuntimeModule) Deliver(ctx context.Context, limit int) (int, error) {
	if m.store == nil || limit <= 0 {
		return 0, nil
	}
	token := uuid.NewString()
	intents, err := m.store.ClaimV6Outbox(ctx, token, 45*time.Second, limit)
	if err != nil {
		return 0, err
	}
	delivered := 0
	for _, intent := range intents {
		result, domainCompleted, deliveryErr := m.deliverOne(ctx, intent, token)
		if deliveryErr != nil {
			_ = m.store.RescheduleV6Outbox(context.WithoutCancel(ctx), intent.ID, token, deliveryErr.Error(), m.clock.Now().Add(time.Minute))
			continue
		}
		if domainCompleted {
			delivered++
			continue
		}
		if err = m.store.CompleteV6Outbox(context.WithoutCancel(ctx), intent.ID, token, result); err != nil {
			return delivered, err
		}
		delivered++
	}
	return delivered, nil
}

func (m v6RuntimeModule) deliverOne(ctx context.Context, intent V6OutboxIntent, token string) (json.RawMessage, bool, error) {
	switch intent.Kind {
	case "create_agent":
		if m.agents == nil || m.team == nil {
			return nil, false, ErrV6DirectorUnavailable
		}
		var payload struct {
			Spec       V6AgentSpec          `json:"spec"`
			Membership AddV6TeamMemberInput `json:"membership"`
		}
		if err := json.Unmarshal(intent.Payload, &payload); err != nil {
			return nil, false, fmt.Errorf("%w: create agent intent", ErrInvalidContract)
		}
		agentID, err := m.agents.CreateAgent(ctx, intent.WorkspaceID, intent.IdempotencyKey, payload.Spec)
		if err != nil {
			return nil, false, err
		}
		payload.Membership.AgentID = agentID
		member, err := m.team.AddV6TeamMember(ctx, payload.Membership)
		if err != nil {
			return nil, false, err
		}
		result, err := json.Marshal(map[string]any{"agent_id": agentID, "membership_id": member.ID})
		return result, false, err
	case "archive_agent":
		if m.agents == nil || m.team == nil {
			return nil, false, ErrV6DirectorUnavailable
		}
		var payload struct {
			AgentID      string `json:"agent_id"`
			MembershipID string `json:"membership_id"`
			Reason       string `json:"reason"`
		}
		if err := json.Unmarshal(intent.Payload, &payload); err != nil {
			return nil, false, fmt.Errorf("%w: archive agent intent", ErrInvalidContract)
		}
		if err := m.agents.ArchiveAgent(ctx, intent.WorkspaceID, payload.AgentID, payload.Reason); err != nil {
			return nil, false, err
		}
		_, err := m.team.ArchiveV6TeamMember(ctx, ArchiveV6TeamMemberInput{
			WorkspaceID: intent.WorkspaceID, RunID: intent.RunID,
			MembershipID: payload.MembershipID, Reason: payload.Reason,
		})
		if err != nil {
			return nil, false, err
		}
		return json.RawMessage(`{"archived":true}`), false, nil
	case "dispatch_work_item":
		if m.inbox == nil {
			return nil, false, ErrV6DirectorUnavailable
		}
		var payload V6DispatchIntentPayload
		if json.Unmarshal(intent.Payload, &payload) != nil {
			return nil, false, fmt.Errorf("%w: dispatch intent", ErrInvalidContract)
		}
		inboxTaskID, err := m.inbox.DispatchV6Work(ctx, payload.Access, V6WorkManifest{Bytes: payload.Manifest}, intent.IdempotencyKey)
		if err != nil {
			return nil, false, err
		}
		if err = m.store.CompleteV6DispatchOutbox(context.WithoutCancel(ctx), intent.ID, token, inboxTaskID); err != nil {
			return nil, false, err
		}
		return nil, true, nil
	default:
		return nil, false, fmt.Errorf("unsupported V6 outbox intent %q", intent.Kind)
	}
}
