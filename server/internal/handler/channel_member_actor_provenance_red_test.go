package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/multica-ai/multica/server/internal/service"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// This file is the actor-provenance RED slice for task #844. It intentionally
// stays below the future human/agent write adapters: the migration, every
// existing channel_member writer, trigger provenance, env-dispatch copy, and
// onboarding's mechanical human creator must be truthful before an agent write
// route can safely exist.

func requireChannelMemberActorProvenanceSchema(t *testing.T) {
	t.Helper()
	if testPool == nil {
		t.Skip("database not available")
	}
	var columns, legacyColumns int
	if err := testPool.QueryRow(context.Background(), `
		SELECT count(*) FILTER (
		         WHERE
		           (table_name = 'channel_member' AND column_name IN ('added_by_type', 'added_by_id'))
		           OR
		           (table_name = 'channel_agent_onboarding' AND column_name IN ('source_actor_type', 'source_actor_id'))
		       ),
		       count(*) FILTER (
		         WHERE table_name = 'channel_member' AND column_name = 'added_by'
		       )
		FROM information_schema.columns
		WHERE table_schema = current_schema()`).Scan(&columns, &legacyColumns); err != nil {
		t.Fatalf("inspect actor provenance schema: %v", err)
	}
	if columns != 4 {
		t.Fatalf("actor-neutral provenance columns = %d, want 4", columns)
	}
	if legacyColumns != 0 {
		t.Fatalf("legacy channel_member.added_by columns = %d, want clean replacement", legacyColumns)
	}
}

func requireChannelMembershipActor(t *testing.T, channelID, memberType, memberID, wantType, wantID string) {
	t.Helper()
	var actorType, actorID string
	if err := testPool.QueryRow(context.Background(), `
		SELECT added_by_type, COALESCE(added_by_id::text, '')
		FROM channel_member
		WHERE channel_id = $1 AND member_type = $2 AND member_id = $3`,
		channelID, memberType, memberID).Scan(&actorType, &actorID); err != nil {
		t.Fatalf("load channel member actor provenance: %v", err)
	}
	if actorType != wantType || actorID != wantID {
		t.Fatalf("channel member actor = %s/%s, want %s/%s", actorType, actorID, wantType, wantID)
	}
}

func TestExistingChannelMemberWritersRecordCanonicalActorProvenance(t *testing.T) {
	requireChannelMemberActorProvenanceSchema(t)

	t.Run("ordinary group database seed is the creating user", func(t *testing.T) {
		var channelID string
		if err := testPool.QueryRow(context.Background(), `
			INSERT INTO channel (workspace_id, name, created_by, kind)
			VALUES ($1, $2, $3, 'group')
			RETURNING id::text`,
			testWorkspaceID, "owner-provenance-"+uuid.NewString(), testUserID).Scan(&channelID); err != nil {
			t.Fatalf("create ordinary group: %v", err)
		}
		t.Cleanup(func() {
			_, _ = testPool.Exec(context.Background(), `DELETE FROM channel WHERE id = $1`, channelID)
		})
		requireChannelMembershipActor(t, channelID, "user", testUserID, "user", testUserID)
	})

	t.Run("ordinary group helper owner is the creating user", func(t *testing.T) {
		ctx := context.Background()
		tx, err := testPool.Begin(ctx)
		if err != nil {
			t.Fatalf("begin helper owner writer: %v", err)
		}
		defer tx.Rollback(ctx)
		if _, err := tx.Exec(ctx, `SET LOCAL session_replication_role = replica`); err != nil {
			t.Fatalf("disable channel seed trigger: %v", err)
		}
		var channelID string
		if err := tx.QueryRow(ctx, `
			INSERT INTO channel (workspace_id, name, created_by, kind)
			VALUES ($1, $2, $3, 'group')
			RETURNING id::text`,
			testWorkspaceID, "owner-helper-provenance-"+uuid.NewString(), testUserID).Scan(&channelID); err != nil {
			t.Fatalf("create helper-owned group: %v", err)
		}
		if _, err := tx.Exec(ctx, `SET LOCAL session_replication_role = origin`); err != nil {
			t.Fatalf("restore channel seed trigger: %v", err)
		}
		if err := insertChannelHumanOwnerTx(ctx, tx, channelID, parseUUID(testWorkspaceID), parseUUID(testUserID)); err != nil {
			t.Fatalf("insert helper-owned membership: %v", err)
		}
		if err := tx.Commit(ctx); err != nil {
			t.Fatalf("commit helper owner writer: %v", err)
		}
		t.Cleanup(func() {
			_, _ = testPool.Exec(context.Background(), `DELETE FROM channel WHERE id = $1`, channelID)
		})
		requireChannelMembershipActor(t, channelID, "user", testUserID, "user", testUserID)
	})

	t.Run("human manual add records the requesting user", func(t *testing.T) {
		targetID := createChannelPlainMember(t)
		channelID := seedChannelForTest(t, "human-add-provenance-"+uuid.NewString(), testUserID)
		req := newRequestAs(testUserID, http.MethodPost, "/api/channels/"+channelID+"/members", AddChannelMemberRequest{
			MemberType: "user",
			MemberID:   targetID,
		})
		req = withChannelTestWorkspaceCtx(t, req, testUserID)
		req = withURLParam(req, "channelId", channelID)
		rec := httptest.NewRecorder()
		testHandler.AddChannelMember(rec, req)
		if rec.Code != http.StatusCreated {
			t.Fatalf("human add status=%d body=%s", rec.Code, rec.Body.String())
		}
		requireChannelMembershipActor(t, channelID, "user", targetID, "user", testUserID)
	})

	t.Run("human batch add records the requesting user for every inserted target", func(t *testing.T) {
		firstTarget := createChannelPlainMember(t)
		secondTarget := createChannelPlainMember(t)
		channelID := seedChannelForTest(t, "human-batch-provenance-"+uuid.NewString(), testUserID)
		req := newRequestAs(testUserID, http.MethodPost, "/api/channels/"+channelID+"/members/batch", AddChannelMembersRequest{
			Members: []AddChannelMemberRequest{
				{MemberType: "user", MemberID: firstTarget},
				{MemberType: "user", MemberID: secondTarget},
			},
		})
		req = withChannelTestWorkspaceCtx(t, req, testUserID)
		req = withURLParam(req, "channelId", channelID)
		rec := httptest.NewRecorder()
		testHandler.AddChannelMembers(rec, req)
		if rec.Code != http.StatusCreated {
			t.Fatalf("human batch add status=%d body=%s", rec.Code, rec.Body.String())
		}
		requireChannelMembershipActor(t, channelID, "user", firstTarget, "user", testUserID)
		requireChannelMembershipActor(t, channelID, "user", secondTarget, "user", testUserID)
	})

	t.Run("dm members record the human creator", func(t *testing.T) {
		targetAgent := createHandlerTestAgent(t, "DM Provenance "+uuid.NewString()[:8], nil)
		canonical := "dm-provenance-" + uuid.NewString()
		channel, created := testHandler.createDMChannel(
			context.Background(), httptest.NewRecorder(), testWorkspaceID, testUserID, canonical,
			[]dmMember{
				{memberType: "user", memberID: parseUUID(testUserID)},
				{memberType: "agent", memberID: parseUUID(targetAgent)},
			},
		)
		if !created {
			t.Fatal("createDMChannel did not create the provenance fixture")
		}
		t.Cleanup(func() {
			_, _ = testPool.Exec(context.Background(), `DELETE FROM channel WHERE id = $1`, channel.ID)
		})
		requireChannelMembershipActor(t, channel.ID, "user", testUserID, "user", testUserID)
		requireChannelMembershipActor(t, channel.ID, "agent", targetAgent, "user", testUserID)
	})

	t.Run("env dispatch create records user owner and system roster actors", func(t *testing.T) {
		ctx, _, envID, _, targetAgent := setupEnvDispatchChannelStoreFixture(t)
		projectID := projectIDForEnvDispatchStoreFixture(t, ctx, envID)
		adapter := &envDispatchDepsAdapter{h: testHandler}
		channelID, err := adapter.CreateEnvDispatchChannel(
			ctx, testWorkspaceID, testUserID, projectID, envID,
			service.MessageRoster{LeaderID: targetAgent, AgentIDs: []string{targetAgent}}, nil,
		)
		if err != nil {
			t.Fatalf("create env-dispatch channel: %v", err)
		}
		t.Cleanup(func() {
			_, _ = testPool.Exec(context.Background(), `DELETE FROM channel WHERE id = $1`, channelID)
		})
		requireChannelMembershipActor(t, channelID, "user", testUserID, "user", testUserID)
		requireChannelMembershipActor(t, channelID, "agent", targetAgent, "system", "")
	})
}

func TestChannelMemberActorResolverRejectsUnknownAndCrossWorkspaceActors(t *testing.T) {
	requireChannelMemberActorProvenanceSchema(t)
	foreignWorkspace := createOtherTestWorkspace(t)
	foreignUser := insertUserAndMember(t, foreignWorkspace)
	foreignAgent := createForeignWorkspaceAgent(t)

	for _, tc := range []struct {
		name      string
		actorType string
		actorID   string
	}{
		{name: "nonexistent user", actorType: "user", actorID: uuid.NewString()},
		{name: "cross workspace user", actorType: "user", actorID: foreignUser},
		{name: "nonexistent agent", actorType: "agent", actorID: uuid.NewString()},
		{name: "cross workspace agent", actorType: "agent", actorID: foreignAgent},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			// This locks only the production resolver's actor-existence
			// contract. Transactional zero-side-effects and proof that every
			// future writer invokes the shared boundary belong to the GREEN
			// write-service/adapter slice; this test does not synthesize a
			// writer call and must not be reported as that proof.
			actorRef := testHandler.channelMemberSystemEventActorRefWithExec(
				ctx, testPool, testWorkspaceID, tc.actorType, parseUUID(tc.actorID),
			)
			if actorRef.Type != "" {
				t.Fatalf(
					"invalid actor %s/%s resolved=%q, want empty actor ref",
					tc.actorType, tc.actorID, actorRef.Type,
				)
			}
		})
	}
}

func TestValidateChannelMemberActorRejectsUnknownAndCrossWorkspaceActors(t *testing.T) {
	requireChannelMemberActorProvenanceSchema(t)
	foreignWorkspace := createOtherTestWorkspace(t)
	foreignUser := insertUserAndMember(t, foreignWorkspace)
	foreignAgent := createForeignWorkspaceAgent(t)

	for _, tc := range []struct {
		name      string
		actorType string
		actorID   string
	}{
		{name: "nonexistent user", actorType: channelMemberActorUser, actorID: uuid.NewString()},
		{name: "cross workspace user", actorType: channelMemberActorUser, actorID: foreignUser},
		{name: "nonexistent agent", actorType: channelMemberActorAgent, actorID: uuid.NewString()},
		{name: "cross workspace agent", actorType: channelMemberActorAgent, actorID: foreignAgent},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := validateChannelMemberActorWithExec(
				context.Background(),
				testPool,
				testWorkspaceID,
				channelMemberActor{Type: tc.actorType, ID: parseUUID(tc.actorID)},
			)
			if err == nil || !strings.Contains(err.Error(), "not an existing same-workspace actor") {
				t.Fatalf(
					"validate actor %s/%s error = %v, want same-workspace rejection",
					tc.actorType,
					tc.actorID,
					err,
				)
			}
		})
	}
}

func TestChannelMemberSystemEventPublicTypePreservesEventActorVocabulary(t *testing.T) {
	for _, tc := range []struct {
		name      string
		actorType string
		want      string
	}{
		{name: "canonical provenance user", actorType: channelMemberActorUser, want: "human"},
		{name: "existing event member", actorType: "member", want: "human"},
		{name: "canonical provenance agent", actorType: channelMemberActorAgent, want: "agent"},
		{name: "canonical provenance system", actorType: channelMemberActorSystem, want: "system"},
		{name: "unknown actor stays rejected", actorType: "owner", want: ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := channelMemberSystemEventPublicType(tc.actorType); got != tc.want {
				t.Fatalf("public actor type for %q = %q, want %q", tc.actorType, got, tc.want)
			}
		})
	}
}

func TestChannelMemberActorProvenanceShapeFailsClosed(t *testing.T) {
	requireChannelMemberActorProvenanceSchema(t)
	channelID := seedChannelForTest(t, "actor-shape-"+uuid.NewString(), testUserID)

	for _, tc := range []struct {
		name      string
		actorType string
		actorID   any
	}{
		{name: "user requires id", actorType: "user", actorID: nil},
		{name: "agent requires id", actorType: "agent", actorID: nil},
		{name: "system forbids id", actorType: "system", actorID: testUserID},
		{name: "unknown actor type", actorType: "owner", actorID: testUserID},
	} {
		t.Run(tc.name, func(t *testing.T) {
			targetID := createChannelPlainMember(t)
			_, err := testPool.Exec(context.Background(), `
				INSERT INTO channel_member (
				  channel_id, workspace_id, member_type, member_id,
				  added_by_type, added_by_id, join_source
				)
				VALUES ($1, $2, 'user', $3, $4, $5, 'manual')`,
				channelID, testWorkspaceID, targetID, tc.actorType, tc.actorID)
			if err == nil {
				t.Fatalf("invalid actor shape %s/%v unexpectedly inserted", tc.actorType, tc.actorID)
			}
			assertChannelUserMembershipCount(t, channelID, targetID, 0)
		})
	}
}

type agentAuthoredOnboardingFixture struct {
	channelID    string
	targetAgent  string
	actorAgent   string
	onboardingID string
	generationID string
	runtimeID    string
}

func seedAgentAuthoredOnboarding(t *testing.T, channelID string) agentAuthoredOnboardingFixture {
	t.Helper()
	requireChannelMemberActorProvenanceSchema(t)
	ctx := context.Background()
	targetAgent := createHandlerTestAgent(t, "Provenance Target "+uuid.NewString()[:8], nil)
	actorAgent := createHandlerTestAgent(t, "Provenance Actor "+uuid.NewString()[:8], nil)
	var generationID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO channel_member (
		  channel_id, workspace_id, member_type, member_id, role,
		  added_by_type, added_by_id, join_source
		)
		VALUES ($1, $2, 'agent', $3, 'member', 'agent', $4, 'manual')
		RETURNING generation_id::text`,
		channelID, testWorkspaceID, targetAgent, actorAgent).Scan(&generationID); err != nil {
		t.Fatalf("insert agent-authored membership: %v", err)
	}
	var onboardingID string
	if err := testPool.QueryRow(ctx, `
		SELECT id::text
		FROM channel_agent_onboarding
		WHERE channel_id = $1 AND agent_id = $2 AND membership_generation_id = $3`,
		channelID, targetAgent, generationID).Scan(&onboardingID); err != nil {
		t.Fatalf("load agent-authored onboarding: %v", err)
	}
	if _, err := testPool.Exec(ctx, `
		UPDATE channel_agent_onboarding
		SET status = 'expired',
		    terminal_evidence = '{"reason":"test_superseded"}'::jsonb,
		    terminal_at = now(),
		    updated_at = now()
		WHERE id <> $1
		  AND status IN ('pending', 'claimed')
		  AND agent_id IN (
		    SELECT id
		    FROM agent
		    WHERE runtime_id = $2
		  )`,
		onboardingID, handlerTestRuntimeID(t)); err != nil {
		t.Fatalf("expire unrelated runtime onboarding: %v", err)
	}
	if err := testHandler.publishChannelOnboardingSystemMessageForGeneration(ctx, parseUUID(generationID)); err != nil {
		t.Fatalf("publish agent-authored onboarding: %v", err)
	}
	if _, err := testPool.Exec(ctx, `
		UPDATE channel_agent_onboarding
		SET created_at = '1970-01-01T00:00:00Z'
		WHERE id = $1`, onboardingID); err != nil {
		t.Fatalf("prioritize agent-authored onboarding: %v", err)
	}
	return agentAuthoredOnboardingFixture{
		channelID:    channelID,
		targetAgent:  targetAgent,
		actorAgent:   actorAgent,
		onboardingID: onboardingID,
		generationID: generationID,
		runtimeID:    handlerTestRuntimeID(t),
	}
}

func requireAgentAuthoredOnboardingEvent(t *testing.T, fixture agentAuthoredOnboardingFixture) {
	t.Helper()
	var sourceType, sourceID, eventActorType, eventActorID string
	if err := testPool.QueryRow(context.Background(), `
		SELECT onboarding.source_actor_type,
		       COALESCE(onboarding.source_actor_id::text, ''),
		       COALESCE(message.parts->0->'event_params'->>'actor_type', ''),
		       COALESCE(message.parts->0->'event_params'->>'actor_id', '')
		FROM channel_agent_onboarding onboarding
		JOIN channel_message message ON message.id = onboarding.system_message_id
		WHERE onboarding.id = $1`, fixture.onboardingID).Scan(
		&sourceType, &sourceID, &eventActorType, &eventActorID,
	); err != nil {
		t.Fatalf("load agent-authored onboarding event: %v", err)
	}
	if sourceType != "agent" || sourceID != fixture.actorAgent {
		t.Fatalf("onboarding source = %s/%s, want agent/%s", sourceType, sourceID, fixture.actorAgent)
	}
	if eventActorType != "agent" || eventActorID != fixture.actorAgent {
		t.Fatalf("system event actor = %s/%s, want agent/%s", eventActorType, eventActorID, fixture.actorAgent)
	}
}

func TestAgentAuthoredOnboardingKeepsRealActorWithoutChatSession(t *testing.T) {
	requireChannelMemberActorProvenanceSchema(t)
	ctx := context.Background()
	historicalCreator := createChannelPlainMember(t)
	currentOwner := createChannelPlainMember(t)
	channelID := seedChannelForTest(t, "current-owner-provenance-"+uuid.NewString(), historicalCreator)
	fixture := seedAgentAuthoredOnboarding(t, channelID)
	requireAgentAuthoredOnboardingEvent(t, fixture)

	tx, err := testPool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin ownership transfer fixture: %v", err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `
		UPDATE channel_member
		SET role = 'member'
		WHERE channel_id = $1 AND workspace_id = $2
		  AND member_type = 'user' AND member_id = $3`,
		channelID, testWorkspaceID, historicalCreator); err != nil {
		t.Fatalf("demote historical creator: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO channel_member (
		  channel_id, workspace_id, member_type, member_id, role,
		  added_by_type, added_by_id, join_source
		)
		VALUES ($1, $2, 'user', $3, 'owner', 'user', $3, 'manual')
		ON CONFLICT (channel_id, member_type, member_id)
		DO UPDATE SET role = 'owner'`,
		channelID, testWorkspaceID, currentOwner); err != nil {
		t.Fatalf("promote current owner: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		DELETE FROM channel_member
		WHERE channel_id = $1 AND workspace_id = $2
		  AND member_type = 'user' AND member_id = $3`,
		channelID, testWorkspaceID, historicalCreator); err != nil {
		t.Fatalf("remove historical creator from channel: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit ownership transfer fixture: %v", err)
	}
	if _, err := testPool.Exec(ctx, `
		DELETE FROM member
		WHERE workspace_id = $1 AND user_id = $2`,
		testWorkspaceID, historicalCreator); err != nil {
		t.Fatalf("remove historical creator from workspace: %v", err)
	}

	runtime, err := testHandler.Queries.GetAgentRuntime(ctx, parseUUID(fixture.runtimeID))
	if err != nil {
		t.Fatalf("load target runtime: %v", err)
	}
	if err := testHandler.materializeNextChannelOnboardingForRuntime(ctx, runtime); err != nil {
		t.Fatalf("materialize agent-authored onboarding: %v", err)
	}

	var rawContext []byte
	if err := testPool.QueryRow(ctx, `
		SELECT context
		FROM agent_inbox_event
		WHERE channel_onboarding_id = $1`, fixture.onboardingID).Scan(&rawContext); err != nil {
		t.Fatalf("load materialized onboarding context: %v", err)
	}
	var wake channelWakeContext
	if err := json.Unmarshal(rawContext, &wake); err != nil {
		t.Fatalf("decode materialized onboarding context: %v", err)
	}
	if !strings.Contains(wake.Prompt, fixture.actorAgent) {
		t.Fatalf("onboarding prompt lost real agent actor %s: %s", fixture.actorAgent, wake.Prompt)
	}
	if strings.Contains(wake.Prompt, historicalCreator) {
		t.Fatalf("onboarding prompt exposed historical creator as action actor: %s", wake.Prompt)
	}
	var sessionCount int
	if err := testPool.QueryRow(ctx, `
		SELECT count(*)
		FROM channel_agent_session
		WHERE channel_id = $1 AND agent_id = $2`, channelID, fixture.targetAgent).Scan(&sessionCount); err != nil {
		t.Fatalf("count legacy onboarding sessions: %v", err)
	}
	if sessionCount != 0 {
		t.Fatalf("onboarding created %d legacy chat sessions, want none", sessionCount)
	}
}

func forceChannelOwnerStateForProvenanceTest(t *testing.T, fixture agentAuthoredOnboardingFixture, state string) {
	t.Helper()
	ctx := context.Background()
	switch state {
	case "duplicate":
		// The connection-private shadow used below supplies the duplicate
		// owner without weakening the real table's owner constraint.
	case "missing":
		conn, err := testPool.Acquire(ctx)
		if err != nil {
			t.Fatalf("acquire owner corruption connection: %v", err)
		}
		defer conn.Release()
		if _, err := conn.Exec(ctx, `SET session_replication_role = replica`); err != nil {
			t.Fatalf("disable owner triggers: %v", err)
		}
		defer conn.Exec(ctx, `SET session_replication_role = DEFAULT`)
		if _, err := conn.Exec(ctx, `
			DELETE FROM channel_member
			WHERE channel_id = $1 AND member_type = 'user' AND role = 'owner'`,
			fixture.channelID); err != nil {
			t.Fatalf("remove current owner: %v", err)
		}
	case "cross_workspace":
		var foreignWorkspace string
		suffix := uuid.NewString()[:8]
		if err := testPool.QueryRow(ctx, `
			INSERT INTO workspace (name, slug, description, issue_prefix)
			VALUES ($1, $2, 'provenance owner negative', 'PO')
			RETURNING id::text`,
			"provenance-owner-"+suffix, "provenance-owner-"+suffix).Scan(&foreignWorkspace); err != nil {
			t.Fatalf("create foreign workspace: %v", err)
		}
		t.Cleanup(func() {
			_, _ = testPool.Exec(context.Background(), `DELETE FROM workspace WHERE id = $1`, foreignWorkspace)
		})
		conn, err := testPool.Acquire(ctx)
		if err != nil {
			t.Fatalf("acquire cross-workspace corruption connection: %v", err)
		}
		defer conn.Release()
		if _, err := conn.Exec(ctx, `SET session_replication_role = replica`); err != nil {
			t.Fatalf("disable owner triggers: %v", err)
		}
		defer conn.Exec(ctx, `SET session_replication_role = DEFAULT`)
		if _, err := conn.Exec(ctx, `
			UPDATE channel_member
			SET workspace_id = $1
			WHERE channel_id = $2 AND member_type = 'user' AND role = 'owner'`,
			foreignWorkspace, fixture.channelID); err != nil {
			t.Fatalf("move owner row outside channel workspace: %v", err)
		}
	default:
		t.Fatalf("unknown owner state %q", state)
	}
}

func materializeChannelOnboardingWithDuplicateOwnerShadow(
	t *testing.T,
	fixture agentAuthoredOnboardingFixture,
	runtime db.AgentRuntime,
) error {
	t.Helper()
	ctx := context.Background()
	connConfig := testPool.Config().ConnConfig.Copy()
	connConfig.DefaultQueryExecMode = pgx.QueryExecModeSimpleProtocol
	conn, err := pgx.ConnectConfig(ctx, connConfig)
	if err != nil {
		t.Fatalf("acquire isolated duplicate-owner connection: %v", err)
	}
	defer func() {
		if _, dropErr := conn.Exec(
			context.Background(),
			`DROP TABLE IF EXISTS pg_temp.channel_member`,
		); dropErr != nil {
			t.Errorf("drop isolated channel_member shadow: %v", dropErr)
		}
		if closeErr := conn.Close(context.Background()); closeErr != nil {
			t.Errorf("close isolated duplicate-owner connection: %v", closeErr)
		}
	}()

	var schema string
	if err := conn.QueryRow(ctx, `SELECT current_schema()`).Scan(&schema); err != nil {
		t.Fatalf("resolve handler test schema: %v", err)
	}
	sourceTable := pgx.Identifier{schema, "channel_member"}.Sanitize()
	if _, err := conn.Exec(ctx, `
		CREATE TEMP TABLE channel_member
		(LIKE `+sourceTable+` INCLUDING DEFAULTS INCLUDING GENERATED INCLUDING IDENTITY)
		ON COMMIT PRESERVE ROWS`); err != nil {
		t.Fatalf("create isolated channel_member shadow: %v", err)
	}
	if _, err := conn.Exec(ctx, `
		INSERT INTO pg_temp.channel_member
		SELECT * FROM `+sourceTable+` WHERE channel_id = $1`,
		fixture.channelID); err != nil {
		t.Fatalf("copy channel memberships into isolated shadow: %v", err)
	}
	secondOwner := createChannelPlainMember(t)
	if _, err := conn.Exec(ctx, `
		INSERT INTO pg_temp.channel_member (
		  channel_id, workspace_id, member_type, member_id, role,
		  added_by_type, added_by_id, join_source
		)
		VALUES ($1, $2, 'user', $3, 'owner', 'user', $3, 'manual')`,
		fixture.channelID, testWorkspaceID, secondOwner); err != nil {
		t.Fatalf("insert duplicate owner into isolated shadow: %v", err)
	}

	isolated := *testHandler
	isolated.DB = conn
	isolated.TxStarter = conn
	isolated.Queries = db.New(conn)
	return isolated.materializeNextChannelOnboardingForRuntime(ctx, runtime)
}

func TestChannelOnboardingDoesNotRequireHumanOwner(t *testing.T) {
	requireChannelMemberActorProvenanceSchema(t)
	for _, state := range []string{"missing", "duplicate", "cross_workspace"} {
		t.Run(state, func(t *testing.T) {
			ctx := context.Background()
			channelID := seedChannelForTest(t, "owner-negative-"+state+"-"+uuid.NewString(), testUserID)
			fixture := seedAgentAuthoredOnboarding(t, channelID)
			forceChannelOwnerStateForProvenanceTest(t, fixture, state)

			var messagesBefore int
			if err := testPool.QueryRow(ctx, `
				SELECT count(*) FROM channel_message WHERE channel_id = $1`,
				channelID).Scan(&messagesBefore); err != nil {
				t.Fatalf("count system messages before materialization: %v", err)
			}
			runtime, err := testHandler.Queries.GetAgentRuntime(ctx, parseUUID(fixture.runtimeID))
			if err != nil {
				t.Fatalf("load target runtime: %v", err)
			}
			if state == "duplicate" {
				err = materializeChannelOnboardingWithDuplicateOwnerShadow(t, fixture, runtime)
			} else {
				err = testHandler.materializeNextChannelOnboardingForRuntime(ctx, runtime)
			}
			if err != nil {
				t.Fatalf("%s owner state must not block onboarding materialization: %v", state, err)
			}

			var sessions, inboxes, messagesAfter int
			var status string
			if err := testPool.QueryRow(ctx, `
				SELECT count(*)
				FROM channel_agent_session
				WHERE channel_id = $1 AND agent_id = $2`,
				channelID, fixture.targetAgent).Scan(&sessions); err != nil {
				t.Fatalf("count channel sessions: %v", err)
			}
			if err := testPool.QueryRow(ctx, `
				SELECT count(*)
				FROM agent_inbox_event
				WHERE channel_onboarding_id = $1`,
				fixture.onboardingID).Scan(&inboxes); err != nil {
				t.Fatalf("count onboarding inbox events: %v", err)
			}
			if err := testPool.QueryRow(ctx, `
				SELECT status
				FROM channel_agent_onboarding
				WHERE id = $1`, fixture.onboardingID).Scan(&status); err != nil {
				t.Fatalf("load onboarding status: %v", err)
			}
			if err := testPool.QueryRow(ctx, `
				SELECT count(*) FROM channel_message WHERE channel_id = $1`,
				channelID).Scan(&messagesAfter); err != nil {
				t.Fatalf("count system messages after materialization: %v", err)
			}
			if sessions != 0 || inboxes != 1 || status != "pending" || messagesAfter != messagesBefore {
				t.Fatalf("%s owner-independent materialization = session:%d inbox:%d status:%s messages:%d->%d",
					state, sessions, inboxes, status, messagesBefore, messagesAfter)
			}
		})
	}
}

func TestChannelOnboardingRejectsNonHumanOwnerBeforeMaterialization(t *testing.T) {
	requireChannelMemberActorProvenanceSchema(t)
	ctx := context.Background()
	channelID := seedChannelForTest(t, "non-human-owner-"+uuid.NewString(), testUserID)
	fixture := seedAgentAuthoredOnboarding(t, channelID)
	if _, err := testPool.Exec(ctx, `
		UPDATE channel_member
		SET role = 'owner'
		WHERE channel_id = $1 AND member_type = 'agent' AND member_id = $2`,
		channelID, fixture.targetAgent); err == nil {
		t.Fatal("agent owner shape unexpectedly passed the database boundary")
	}
	var sessions, inboxes int
	if err := testPool.QueryRow(ctx, `
		SELECT count(*) FROM channel_agent_session
		WHERE channel_id = $1 AND agent_id = $2`,
		channelID, fixture.targetAgent).Scan(&sessions); err != nil {
		t.Fatalf("count non-human-owner sessions: %v", err)
	}
	if err := testPool.QueryRow(ctx, `
		SELECT count(*) FROM agent_inbox_event
		WHERE channel_onboarding_id = $1`,
		fixture.onboardingID).Scan(&inboxes); err != nil {
		t.Fatalf("count non-human-owner inboxes: %v", err)
	}
	if sessions != 0 || inboxes != 0 {
		t.Fatalf("non-human owner attempt mutated sessions=%d inboxes=%d", sessions, inboxes)
	}
}

func TestEnvDispatchChannelCopyPreservesJoinProvenanceWithoutOnboardingWake(t *testing.T) {
	requireChannelMemberActorProvenanceSchema(t)
	ctx := context.Background()
	targetAgent := createHandlerTestAgent(t, "Env Copy Provenance "+uuid.NewString()[:8], nil)
	var sourceProject, destinationProject, sourceChannel string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO project (workspace_id, title)
		VALUES ($1, $2)
		RETURNING id::text`,
		testWorkspaceID, "Env provenance source "+uuid.NewString()).Scan(&sourceProject); err != nil {
		t.Fatalf("create source project: %v", err)
	}
	if err := testPool.QueryRow(ctx, `
		INSERT INTO project (workspace_id, title)
		VALUES ($1, $2)
		RETURNING id::text`,
		testWorkspaceID, "Env provenance destination "+uuid.NewString()).Scan(&destinationProject); err != nil {
		t.Fatalf("create destination project: %v", err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM project WHERE id IN ($1, $2)`, sourceProject, destinationProject)
	})
	if err := testPool.QueryRow(ctx, `
		INSERT INTO channel (workspace_id, name, kind, project_id, created_by)
		VALUES ($1, $2, 'group', $3, $4)
		RETURNING id::text`,
		testWorkspaceID, "env-copy-provenance-"+uuid.NewString(), sourceProject, testUserID).Scan(&sourceChannel); err != nil {
		t.Fatalf("create source env-dispatch channel: %v", err)
	}
	var sourceGeneration string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO channel_member (
		  channel_id, workspace_id, member_type, member_id, role,
		  added_by_type, added_by_id, join_source
		)
		VALUES ($1, $2, 'agent', $3, 'member', 'user', $4, 'env_dispatch')
		RETURNING generation_id::text`,
		sourceChannel, testWorkspaceID, targetAgent, testUserID).Scan(&sourceGeneration); err != nil {
		t.Fatalf("insert source env-dispatch membership: %v", err)
	}

	copied, err := testHandler.copyEnvDispatchChannel(
		ctx,
		testWorkspaceID,
		sourceChannel,
		destinationProject,
		uuid.NewString(),
	)
	if err != nil {
		t.Fatalf("copy env-dispatch channel: %v", err)
	}
	var joinSource, actorType, actorID, destinationGeneration string
	if err := testPool.QueryRow(ctx, `
		SELECT join_source, added_by_type, COALESCE(added_by_id::text, ''), generation_id::text
		FROM channel_member
		WHERE channel_id = $1 AND member_type = 'agent' AND member_id = $2`,
		copied.ChannelID, targetAgent).Scan(
		&joinSource, &actorType, &actorID, &destinationGeneration,
	); err != nil {
		t.Fatalf("load copied env-dispatch membership: %v", err)
	}
	if joinSource != "env_dispatch" || actorType != "user" || actorID != testUserID {
		t.Fatalf("copied env-dispatch identity = %s %s/%s", joinSource, actorType, actorID)
	}
	if destinationGeneration == sourceGeneration {
		t.Fatalf("copied membership reused source generation %s", sourceGeneration)
	}
	var onboardingRows int
	if err := testPool.QueryRow(ctx, `
		SELECT count(*)
		FROM channel_agent_onboarding
		WHERE channel_id = $1 AND agent_id = $2`,
		copied.ChannelID, targetAgent).Scan(&onboardingRows); err != nil {
		t.Fatalf("count copied env-dispatch onboarding: %v", err)
	}
	if onboardingRows != 0 {
		t.Fatalf("copied historical env-dispatch roster woke %d onboarding rows", onboardingRows)
	}
}
