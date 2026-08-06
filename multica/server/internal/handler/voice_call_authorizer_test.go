package handler

import (
	"context"
	"errors"
	"testing"

	"github.com/multica-ai/multica/server/internal/service/voicecall"
)

func TestVoiceCallDMAuthorizerEnforcesCanonicalDMAgentScope(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	agentID := createHandlerTestAgent(t, "voice-call-authorizer", []byte("[]"))
	otherAgentID := createHandlerTestAgent(t, "voice-call-authorizer-other", []byte("[]"))
	channelID := seedAgentDMChannel(t, agentID)
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM channel WHERE id = $1`, channelID)
	})

	authorizer, err := NewVoiceCallDMAuthorizer(testHandler)
	if err != nil {
		t.Fatalf("create authorizer: %v", err)
	}
	scope := voicecall.Scope{
		WorkspaceID: testWorkspaceID,
		ChannelID:   channelID,
		AgentID:     agentID,
		UserID:      testUserID,
	}
	if err := authorizer.Authorize(context.Background(), scope); err != nil {
		t.Fatalf("authorize canonical agent DM: %v", err)
	}

	scope.AgentID = otherAgentID
	if err := authorizer.Authorize(context.Background(), scope); !errors.Is(err, voicecall.ErrScopeNotFound) {
		t.Fatalf("mismatched DM agent error = %v, want scope not found", err)
	}
}

func TestVoiceCallDMAuthorizerRejectsArchivedDM(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	agentID := createHandlerTestAgent(t, "voice-call-archived-dm", []byte("[]"))
	channelID := seedAgentDMChannel(t, agentID)
	if _, err := testPool.Exec(context.Background(), `
		UPDATE channel SET archived_at = now(), archived_by = $2 WHERE id = $1`,
		channelID, testUserID,
	); err != nil {
		t.Fatalf("archive DM: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM channel WHERE id = $1`, channelID)
	})

	authorizer, err := NewVoiceCallDMAuthorizer(testHandler)
	if err != nil {
		t.Fatalf("create authorizer: %v", err)
	}
	err = authorizer.Authorize(context.Background(), voicecall.Scope{
		WorkspaceID: testWorkspaceID,
		ChannelID:   channelID,
		AgentID:     agentID,
		UserID:      testUserID,
	})
	if !errors.Is(err, voicecall.ErrScopeUnavailable) {
		t.Fatalf("archived DM error = %v, want scope unavailable", err)
	}
}

// TestVoiceCallDMAuthorizerUnconditionalPostBatch908 supersedes the old
// "reuses private agent access" regression: task #908 makes inviting an
// agent to a voice call a "usage" surface, unconditional for every workspace
// member with their own DM channel to that agent — same as chat/DM/mention.
func TestVoiceCallDMAuthorizerUnconditionalPostBatch908(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	agentID, ownerID, memberID := privateAgentTestFixture(t)
	ownerChannelID := seedAgentDMChannelForUser(t, ownerID, agentID)
	memberChannelID := seedAgentDMChannelForUser(t, memberID, agentID)
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM channel WHERE id = ANY($1::uuid[])`, []string{
			ownerChannelID,
			memberChannelID,
		})
	})

	authorizer, err := NewVoiceCallDMAuthorizer(testHandler)
	if err != nil {
		t.Fatalf("create authorizer: %v", err)
	}
	ownerScope := voicecall.Scope{
		WorkspaceID: testWorkspaceID,
		ChannelID:   ownerChannelID,
		AgentID:     agentID,
		UserID:      ownerID,
	}
	if err := authorizer.Authorize(context.Background(), ownerScope); err != nil {
		t.Fatalf("agent owner should be authorized: %v", err)
	}

	memberScope := ownerScope
	memberScope.ChannelID = memberChannelID
	memberScope.UserID = memberID
	if err := authorizer.Authorize(context.Background(), memberScope); err != nil {
		t.Fatalf("plain member with their own DM channel should be authorized (unconditional post-#908): %v", err)
	}
}
