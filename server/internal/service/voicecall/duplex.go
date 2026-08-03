package voicecall

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// DuplexActivation carries the durable session and its localized, identity-aware greeting.
type DuplexActivation struct {
	Session        Session
	WelcomeMessage string
}

// ActivateDuplex promotes a starting voice_call_session to active without
// starting Volcengine VoiceChat. Duplex media is owned by the Doubao gateway;
// the shared session row still scopes Multica agent dispatch (channel/agent).
func (service *Service) ActivateDuplex(ctx context.Context, input AnswerInput) (DuplexActivation, error) {
	input.WorkspaceID = strings.TrimSpace(input.WorkspaceID)
	input.UserID = strings.TrimSpace(input.UserID)
	input.CallID = strings.TrimSpace(input.CallID)
	if input.WorkspaceID == "" || input.UserID == "" || input.CallID == "" {
		return DuplexActivation{}, errors.New("voice call workspace, user, and call IDs are required")
	}

	session, err := service.store.Get(ctx, input.WorkspaceID, input.UserID, input.CallID)
	if err != nil {
		return DuplexActivation{}, fmt.Errorf("get voice call for duplex activate: %w", err)
	}
	if err := service.authorizer.Authorize(ctx, sessionScope(session)); err != nil {
		return DuplexActivation{}, fmt.Errorf("authorize duplex activate: %w", err)
	}
	conversationContext, err := service.contextBuilder.Build(ctx, sessionScope(session))
	if err != nil {
		return DuplexActivation{}, fmt.Errorf("build duplex voice call context: %w", err)
	}
	if err := validateConversationContext(conversationContext); err != nil {
		return DuplexActivation{}, err
	}
	switch session.Status {
	case StatusActive, StatusReconnecting:
		if session.ConnectedAt != nil && session.Status == StatusActive {
			return DuplexActivation{
				Session:        session,
				WelcomeMessage: strings.TrimSpace(conversationContext.WelcomeMessage),
			}, nil
		}
	case StatusStarting:
		// Duplex does not start Volcengine VoiceChat, but it still follows the
		// durable call state machine before recording the audible answer.
		// BeginProviderStart owns only the starting -> connecting transition;
		// the caller of Provider.Connect remains the regular RTC path.
		connecting, err := service.store.BeginProviderStart(
			ctx,
			input.WorkspaceID,
			input.CallID,
		)
		if err != nil {
			return DuplexActivation{}, fmt.Errorf("begin duplex voice call connection: %w", err)
		}
		session = connecting.Session
	case StatusConnecting:
	default:
		return DuplexActivation{}, ErrScopeUnavailable
	}

	activated, err := service.store.ApplyClientAnswered(
		ctx,
		input.WorkspaceID,
		input.UserID,
		input.CallID,
	)
	if err != nil {
		if errors.Is(err, ErrCallNotFound) {
			return DuplexActivation{}, ErrScopeUnavailable
		}
		return DuplexActivation{}, fmt.Errorf("activate duplex voice call: %w", err)
	}
	return DuplexActivation{
		Session:        activated,
		WelcomeMessage: strings.TrimSpace(conversationContext.WelcomeMessage),
	}, nil
}

// EndWithoutProviderStop ends a call that never started RTC VoiceChat (Duplex
// media path). Still runs BeginEnding so end_reason is persisted, then skips
// provider.Stop which would otherwise fail for a never-started VoiceChat task.
func (service *Service) EndWithoutProviderStop(ctx context.Context, input StopInput) (Session, error) {
	input.WorkspaceID = strings.TrimSpace(input.WorkspaceID)
	input.UserID = strings.TrimSpace(input.UserID)
	input.CallID = strings.TrimSpace(input.CallID)
	input.Reason = strings.TrimSpace(input.Reason)
	if input.WorkspaceID == "" || input.UserID == "" || input.CallID == "" || input.Reason == "" {
		return Session{}, errors.New("voice call workspace, user, call IDs, and stop reason are required")
	}

	cleanupContext, cancel := service.newCleanupContext(ctx)
	defer cancel()
	session, err := service.store.Get(cleanupContext, input.WorkspaceID, input.UserID, input.CallID)
	if err != nil {
		return Session{}, fmt.Errorf("get voice call for duplex end: %w", err)
	}
	if err := service.authorizer.Authorize(cleanupContext, sessionScope(session)); err != nil {
		return Session{}, fmt.Errorf("authorize duplex end: %w", err)
	}
	ending, err := service.store.BeginEnding(
		cleanupContext, input.WorkspaceID, input.UserID, input.CallID, input.Reason,
	)
	if err != nil {
		return Session{}, fmt.Errorf("begin ending duplex voice call: %w", err)
	}
	if ending.Session.Status == StatusEnded || ending.Session.Status == StatusFailed {
		return ending.Session, nil
	}
	session, err = service.store.MarkEnded(
		cleanupContext,
		ending.Session.WorkspaceID,
		ending.Session.ID,
		ending.Session.EndReason,
	)
	if err != nil {
		return Session{}, fmt.Errorf("mark duplex voice call ended: %w", err)
	}
	return session, nil
}
