package handler

import (
	"context"
	"errors"
	"fmt"

	"github.com/multica-ai/multica/server/internal/service/voicecall"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// VoiceCallDMAuthorizer applies the same membership and private-agent rules as
// the existing DM message surfaces while also binding a call to the canonical
// human-agent DM pair. Provider prompts and media credentials must never be
// issued for an agent ID supplied outside that pair.
type VoiceCallDMAuthorizer struct {
	handler *Handler
}

var _ voicecall.Authorizer = (*VoiceCallDMAuthorizer)(nil)

func NewVoiceCallDMAuthorizer(handler *Handler) (*VoiceCallDMAuthorizer, error) {
	if handler == nil || handler.DB == nil || handler.Queries == nil {
		return nil, errors.New("voice call DM authorizer requires a configured handler")
	}
	return &VoiceCallDMAuthorizer{handler: handler}, nil
}

func (authorizer *VoiceCallDMAuthorizer) Authorize(ctx context.Context, scope voicecall.Scope) error {
	workspaceID, err := util.ParseUUID(scope.WorkspaceID)
	if err != nil {
		return fmt.Errorf("%w: invalid workspace", voicecall.ErrScopeNotFound)
	}
	channelID, err := util.ParseUUID(scope.ChannelID)
	if err != nil {
		return fmt.Errorf("%w: invalid channel", voicecall.ErrScopeNotFound)
	}
	agentID, err := util.ParseUUID(scope.AgentID)
	if err != nil {
		return fmt.Errorf("%w: invalid agent", voicecall.ErrScopeNotFound)
	}
	userID, err := util.ParseUUID(scope.UserID)
	if err != nil {
		return fmt.Errorf("%w: invalid member", voicecall.ErrScopeForbidden)
	}

	if _, err := authorizer.handler.getWorkspaceMember(ctx, scope.UserID, scope.WorkspaceID); err != nil {
		return fmt.Errorf("%w: member is outside workspace", voicecall.ErrScopeForbidden)
	}
	channel, found := authorizer.handler.getChannel(ctx, scope.WorkspaceID, channelID)
	if !found || channel.Kind != "dm" {
		return fmt.Errorf("%w: direct message does not exist", voicecall.ErrScopeNotFound)
	}
	if channel.ArchivedAt != nil {
		return fmt.Errorf("%w: direct message is archived", voicecall.ErrScopeUnavailable)
	}
	if !authorizer.handler.channelUserIsMember(ctx, scope.WorkspaceID, channelID, userID) {
		return fmt.Errorf("%w: member is outside direct message", voicecall.ErrScopeForbidden)
	}
	expectedName := dmCanonicalName("user", scope.UserID, "agent", scope.AgentID)
	if channel.Name != expectedName {
		return fmt.Errorf("%w: direct message pair does not match", voicecall.ErrScopeNotFound)
	}
	peer, found := authorizer.handler.resolveDMChannelPeerOnly(
		ctx,
		scope.WorkspaceID,
		scope.UserID,
		channelID,
	)
	if !found || peer.Type != "agent" || peer.ID != agentID {
		return fmt.Errorf("%w: direct message agent does not match", voicecall.ErrScopeNotFound)
	}

	agent, err := authorizer.handler.Queries.GetAgentInWorkspace(ctx, db.GetAgentInWorkspaceParams{
		ID:          agentID,
		WorkspaceID: workspaceID,
	})
	if err != nil || agent.ArchivedAt.Valid {
		return fmt.Errorf("%w: agent does not exist", voicecall.ErrScopeNotFound)
	}
	return nil
}
