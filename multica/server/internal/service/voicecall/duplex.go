package voicecall

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// ActivateDuplex promotes a starting voice_call_session to active without
// starting Volcengine VoiceChat. Duplex media is owned by the Doubao gateway;
// the shared session row still scopes Multica agent dispatch (channel/agent).
func (service *Service) ActivateDuplex(ctx context.Context, input AnswerInput) (Session, error) {
	input.WorkspaceID = strings.TrimSpace(input.WorkspaceID)
	input.UserID = strings.TrimSpace(input.UserID)
	input.CallID = strings.TrimSpace(input.CallID)
	if input.WorkspaceID == "" || input.UserID == "" || input.CallID == "" {
		return Session{}, errors.New("voice call workspace, user, and call IDs are required")
	}

	session, err := service.store.Get(ctx, input.WorkspaceID, input.UserID, input.CallID)
	if err != nil {
		return Session{}, fmt.Errorf("get voice call for duplex activate: %w", err)
	}
	if err := service.authorizer.Authorize(ctx, sessionScope(session)); err != nil {
		return Session{}, fmt.Errorf("authorize duplex activate: %w", err)
	}
	switch session.Status {
	case StatusActive, StatusReconnecting:
		if session.ConnectedAt != nil && session.Status == StatusActive {
			return session, nil
		}
	case StatusStarting, StatusConnecting:
	default:
		return Session{}, ErrScopeUnavailable
	}

	activated, err := service.store.ApplyClientAnswered(
		ctx,
		input.WorkspaceID,
		input.UserID,
		input.CallID,
	)
	if err != nil {
		if errors.Is(err, ErrCallNotFound) {
			return Session{}, ErrScopeUnavailable
		}
		return Session{}, fmt.Errorf("activate duplex voice call: %w", err)
	}
	return activated, nil
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
