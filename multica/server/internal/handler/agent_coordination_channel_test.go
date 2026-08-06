package handler

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

func TestParseCoordinationAgentIDsIncludesSourceAndDeduplicates(t *testing.T) {
	source := parseUUID("00000000-0000-0000-0000-000000000002")
	peer := "00000000-0000-0000-0000-000000000001"

	got, ok := parseCoordinationAgentIDs([]string{peer, peer}, source)
	if !ok {
		t.Fatal("parseCoordinationAgentIDs rejected valid UUIDs")
	}
	want := []string{peer, uuidToString(source)}
	if actual := coordinationAgentIDStrings(got); len(actual) != 2 ||
		actual[0] != want[0] || actual[1] != want[1] {
		t.Fatalf("agent ids = %v, want %v", actual, want)
	}
}

func TestParseCoordinationAgentIDsRejectsMalformedInput(t *testing.T) {
	if _, ok := parseCoordinationAgentIDs(
		[]string{"not-an-agent-id"},
		parseUUID("00000000-0000-0000-0000-000000000002"),
	); ok {
		t.Fatal("parseCoordinationAgentIDs accepted malformed UUID")
	}
}

func TestSameAgentCoordinationRequestComparesImmutableShape(t *testing.T) {
	parent := "00000000-0000-0000-0000-000000000010"
	purpose := "review"
	memberIDs := []pgtype.UUID{
		parseUUID("00000000-0000-0000-0000-000000000002"),
		parseUUID("00000000-0000-0000-0000-000000000001"),
	}
	existing := AgentCoordinationChannelResponse{
		Name:            "backend-review",
		MemberAgentIDs:  []string{"00000000-0000-0000-0000-000000000001", "00000000-0000-0000-0000-000000000002"},
		ParentChannelID: &parent,
		Purpose:         &purpose,
	}
	if !sameAgentCoordinationRequest(existing, "backend-review", &parent, &purpose, memberIDs) {
		t.Fatal("same request was treated as conflicting")
	}
	differentPurpose := "research"
	if sameAgentCoordinationRequest(existing, "backend-review", &parent, &differentPurpose, memberIDs) {
		t.Fatal("different purpose was treated as the same request")
	}
}

func TestCreateAgentCoordinationChannelPersistsOwnerMembersAndIdempotency(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	sourceID := createHandlerTestAgent(t, "coord-source-"+uuid.NewString()[:8], nil)
	peerID := createHandlerTestAgent(t, "coord-peer-"+uuid.NewString()[:8], nil)
	name := "coordination-" + uuid.NewString()[:8]
	requestID := "coord-request-" + uuid.NewString()
	purpose := "parallel-review"
	members := []pgtype.UUID{parseUUID(sourceID), parseUUID(peerID)}

	created, wasCreated, err := testHandler.createAgentCoordinationChannel(
		ctx,
		parseUUID(testWorkspaceID),
		parseUUID(sourceID),
		parseUUID(testUserID),
		name,
		nil,
		pgtype.UUID{},
		nil,
		&purpose,
		requestID,
		members,
	)
	if err != nil {
		t.Fatalf("create coordination channel: %v", err)
	}
	if !wasCreated || !created.Temporary {
		t.Fatalf("created=%v temporary=%v, want true/true", wasCreated, created.Temporary)
	}

	var temporary bool
	var creatorAgentID, ownerUserID pgtype.UUID
	if err := testPool.QueryRow(ctx, `
		SELECT ch.temporary, ch.created_by_agent_id, owner.member_id
		FROM channel ch
		JOIN channel_member owner
		  ON owner.channel_id = ch.id
		 AND owner.member_type = 'user'
		 AND owner.role = 'owner'
		WHERE ch.id = $1`, parseUUID(created.ChannelID)).Scan(
		&temporary, &creatorAgentID, &ownerUserID,
	); err != nil {
		t.Fatalf("load coordination channel owner: %v", err)
	}
	if !temporary || uuidToString(creatorAgentID) != sourceID || uuidToString(ownerUserID) != testUserID {
		t.Fatalf("temporary=%v creator=%s owner=%s", temporary, uuidToString(creatorAgentID), uuidToString(ownerUserID))
	}
	var agentMemberCount int
	if err := testPool.QueryRow(ctx, `
		SELECT count(*) FROM channel_member
		WHERE channel_id = $1 AND member_type = 'agent'`,
		parseUUID(created.ChannelID)).Scan(&agentMemberCount); err != nil {
		t.Fatalf("count Agent members: %v", err)
	}
	if agentMemberCount != 2 {
		t.Fatalf("Agent member count=%d, want 2", agentMemberCount)
	}

	retried, wasCreated, err := testHandler.createAgentCoordinationChannel(
		ctx,
		parseUUID(testWorkspaceID),
		parseUUID(sourceID),
		parseUUID(testUserID),
		name,
		nil,
		pgtype.UUID{},
		nil,
		&purpose,
		requestID,
		members,
	)
	if err != nil {
		t.Fatalf("retry coordination channel: %v", err)
	}
	if wasCreated || retried.ChannelID != created.ChannelID {
		t.Fatalf("retry created=%v id=%s, want false/%s", wasCreated, retried.ChannelID, created.ChannelID)
	}

	differentPurpose := "different"
	if _, _, err := testHandler.createAgentCoordinationChannel(
		ctx,
		parseUUID(testWorkspaceID),
		parseUUID(sourceID),
		parseUUID(testUserID),
		name,
		nil,
		pgtype.UUID{},
		nil,
		&differentPurpose,
		requestID,
		members,
	); err != errAgentCoordinationConflict {
		t.Fatalf("conflicting retry error=%v, want %v", err, errAgentCoordinationConflict)
	}
}

func TestArchiveAgentCoordinationChannelOnlyAllowsCreatingAgentAndOwnerProvenance(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	creatorID := createHandlerTestAgent(t, "coord-archive-creator-"+uuid.NewString()[:8], nil)
	otherID := createHandlerTestAgent(t, "coord-archive-other-"+uuid.NewString()[:8], nil)
	name := "coord-archive-" + uuid.NewString()[:8]
	created, _, err := testHandler.createAgentCoordinationChannel(
		ctx,
		parseUUID(testWorkspaceID),
		parseUUID(creatorID),
		parseUUID(testUserID),
		name,
		nil,
		pgtype.UUID{},
		nil,
		nil,
		"archive-"+uuid.NewString(),
		[]pgtype.UUID{parseUUID(creatorID), parseUUID(otherID)},
	)
	if err != nil {
		t.Fatalf("create coordination channel: %v", err)
	}

	if _, err := testHandler.archiveAgentCoordinationChannel(
		ctx,
		parseUUID(testWorkspaceID),
		parseUUID(created.ChannelID),
		parseUUID(otherID),
		parseUUID(testUserID),
	); err != errAgentCoordinationForbidden {
		t.Fatalf("other Agent archive error=%v, want forbidden", err)
	}
	if _, err := testHandler.archiveAgentCoordinationChannel(
		ctx,
		parseUUID(testWorkspaceID),
		parseUUID(created.ChannelID),
		parseUUID(creatorID),
		parseUUID(uuid.NewString()),
	); err != errAgentCoordinationForbidden {
		t.Fatalf("wrong human provenance archive error=%v, want forbidden", err)
	}
	archived, err := testHandler.archiveAgentCoordinationChannel(
		ctx,
		parseUUID(testWorkspaceID),
		parseUUID(created.ChannelID),
		parseUUID(creatorID),
		parseUUID(testUserID),
	)
	if err != nil {
		t.Fatalf("creator archive: %v", err)
	}
	if archived.ArchivedAt == nil {
		t.Fatal("archived_at is nil after archive")
	}
}
