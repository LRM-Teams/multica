package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sort"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

const channelMemberRoleChangedContractEvent = "channel_member_role_changed"
const channelRoleChangedWakeContractReason = "channel_role_changed"

type managerChannelContract struct {
	ID   string
	Name string
}

type roleWakeFixture struct {
	channel ChannelResponse
	agentA  string
	agentB  string
}

func newRoleWakeFixture(t *testing.T) roleWakeFixture {
	t.Helper()
	if testHandler == nil || testPool == nil {
		t.Skip("handler test DB not configured")
	}

	req := newRequestAs(testUserID, http.MethodPost, "/api/channels", map[string]any{
		"name": "manager-role-wake-" + uuid.NewString()[:8],
	})
	req = withChannelTestWorkspaceCtx(t, req, testUserID)
	rec := httptest.NewRecorder()
	testHandler.CreateChannel(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create channel status=%d body=%s", rec.Code, rec.Body.String())
	}
	var channel ChannelResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &channel); err != nil {
		t.Fatalf("decode channel: %v", err)
	}

	agentA := createHandlerTestAgent(t, "manager-role-a-"+uuid.NewString(), nil)
	agentB := createHandlerTestAgent(t, "manager-role-b-"+uuid.NewString(), nil)
	if _, err := testPool.Exec(context.Background(), `
		INSERT INTO channel_member (
			channel_id, workspace_id, member_type, member_id, role
		)
		VALUES
			($1, $2, 'agent', $3, 'member'),
			($1, $2, 'agent', $4, 'manager')`,
		channel.ID, testWorkspaceID, agentA, agentB,
	); err != nil {
		t.Fatalf("seed agent members: %v", err)
	}
	// Delete the roster before createHandlerTestAgent's cleanup runs. Otherwise
	// the membership FKs keep the agents and their role-change inbox events
	// alive, contaminating later tests that drain the shared runtime.
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM channel WHERE id = $1`, channel.ID)
	})
	return roleWakeFixture{channel: channel, agentA: agentA, agentB: agentB}
}

func patchChannelMemberRole(
	t *testing.T,
	channelID, memberType, memberID, role string,
) *httptest.ResponseRecorder {
	t.Helper()
	req := newRequestAs(
		testUserID,
		http.MethodPatch,
		"/api/channels/"+channelID+"/members/"+memberType+"/"+memberID,
		map[string]any{"role": role},
	)
	req = withChannelTestWorkspaceCtx(t, req, testUserID)
	req = withRouteParams(
		req,
		"channelId", channelID,
		"memberType", memberType,
		"memberId", memberID,
	)
	rec := httptest.NewRecorder()
	testHandler.UpdateChannelMemberRole(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("PATCH role=%s target=%s/%s status=%d body=%s", role, memberType, memberID, rec.Code, rec.Body.String())
	}
	return rec
}

func roleChangeSystemEvents(
	t *testing.T,
	channelID, targetType, targetID string,
) (count int, previousRole, newRole string) {
	t.Helper()
	err := testPool.QueryRow(context.Background(), `
		SELECT count(*),
		       COALESCE(max(parts->0->'params'->>'previous_role'), ''),
		       COALESCE(max(parts->0->'params'->>'new_role'), '')
		FROM channel_message
		WHERE channel_id = $1
		  AND workspace_id = $2
		  AND author_type = 'system'
		  AND parts->0->>'event' = $3
		  AND parts->0->'params'->>'target_type' = $4
		  AND parts->0->'params'->>'target_id' = $5`,
		channelID,
		testWorkspaceID,
		channelMemberRoleChangedContractEvent,
		targetType,
		targetID,
	).Scan(&count, &previousRole, &newRole)
	if err != nil {
		t.Fatalf("load role-change system events: %v", err)
	}
	return count, previousRole, newRole
}

type roleTransitionContract struct {
	Previous string
	New      string
}

func roleChangeTransitions(
	t *testing.T,
	channelID, targetType, targetID string,
) []roleTransitionContract {
	t.Helper()
	rows, err := testPool.Query(context.Background(), `
		SELECT parts->0->'params'->>'previous_role',
		       parts->0->'params'->>'new_role'
		FROM channel_message
		WHERE channel_id = $1
		  AND workspace_id = $2
		  AND author_type = 'system'
		  AND parts->0->>'event' = $3
		  AND parts->0->'params'->>'target_type' = $4
		  AND parts->0->'params'->>'target_id' = $5
		ORDER BY seq, id`,
		channelID,
		testWorkspaceID,
		channelMemberRoleChangedContractEvent,
		targetType,
		targetID,
	)
	if err != nil {
		t.Fatalf("load ordered role transitions: %v", err)
	}
	defer rows.Close()

	var out []roleTransitionContract
	for rows.Next() {
		var transition roleTransitionContract
		if err := rows.Scan(&transition.Previous, &transition.New); err != nil {
			t.Fatalf("scan ordered role transition: %v", err)
		}
		out = append(out, transition)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate ordered role transitions: %v", err)
	}
	return out
}

func roleChangeWakeCount(
	t *testing.T,
	channelID, agentID string,
) (count int, minPriority int32, maxPriority int32) {
	t.Helper()
	err := testPool.QueryRow(context.Background(), `
		SELECT count(*),
		       COALESCE(min(priority), 0),
		       COALESCE(max(priority), 0)
		FROM agent_inbox_event
		WHERE workspace_id = $1
		  AND channel_id = $2
		  AND agent_id = $3
		  AND reason = $4`,
		testWorkspaceID,
		channelID,
		agentID,
		channelRoleChangedWakeContractReason,
	).Scan(&count, &minPriority, &maxPriority)
	if err != nil {
		t.Fatalf("load role-change wakes: %v", err)
	}
	return count, minPriority, maxPriority
}

func seedOrdinaryRoleContractReminder(t *testing.T, channelID, agentID string) string {
	t.Helper()
	id := uuid.NewString()
	if _, err := testPool.Exec(context.Background(), `
		INSERT INTO agent_reminder (
			id, workspace_id, agent_id, initiator_user_id, title,
			anchor_channel_id, fire_at
		) VALUES ($1, $2, $3, $4, 'ordinary role contract reminder', $5, now() + interval '2 hours')`,
		id, testWorkspaceID, agentID, testUserID, channelID,
	); err != nil {
		t.Fatalf("seed ordinary Reminder: %v", err)
	}
	return id
}

func ordinaryRoleContractReminderSnapshot(t *testing.T, reminderID string) string {
	t.Helper()
	var snapshot string
	if err := testPool.QueryRow(context.Background(), `
		SELECT jsonb_build_object(
			'id', id::text,
			'status', status,
			'version', version,
			'title', title,
			'fire_at', fire_at
		)::text
		FROM agent_reminder
		WHERE id = $1`, reminderID).Scan(&snapshot); err != nil {
		t.Fatalf("snapshot ordinary Reminder: %v", err)
	}
	return snapshot
}

func TestAgentManagerRoleTransitionWritesCanonicalEventAndDirectedWake(t *testing.T) {
	fixture := newRoleWakeFixture(t)
	reminderID := seedOrdinaryRoleContractReminder(t, fixture.channel.ID, fixture.agentA)
	reminderBefore := ordinaryRoleContractReminderSnapshot(t, reminderID)
	if testHandler.TaskService == nil {
		t.Skip("task service not configured")
	}
	wakeup := &recordingTaskWakeup{}
	previousWakeup := testHandler.TaskService.Wakeup
	testHandler.TaskService.Wakeup = wakeup
	t.Cleanup(func() { testHandler.TaskService.Wakeup = previousWakeup })

	patchChannelMemberRole(t, fixture.channel.ID, "agent", fixture.agentA, "manager")

	count, previousRole, newRole := roleChangeSystemEvents(
		t, fixture.channel.ID, "agent", fixture.agentA,
	)
	if count != 1 || previousRole != "member" || newRole != "manager" {
		t.Errorf(
			"promotion role events=(count=%d previous=%q new=%q) want (1,member,manager)",
			count, previousRole, newRole,
		)
	}
	wakes, minPriority, maxPriority := roleChangeWakeCount(t, fixture.channel.ID, fixture.agentA)
	if wakes != 1 || minPriority != 10 || maxPriority != 10 {
		t.Errorf(
			"promotion wakes=(count=%d min=%d max=%d) want (1,%d,%d)",
			wakes, minPriority, maxPriority,
			10, 10,
		)
	}
	otherWakes, _, _ := roleChangeWakeCount(t, fixture.channel.ID, fixture.agentB)
	if otherWakes != 0 {
		t.Errorf("promotion of agent A woke agent B %d times want 0", otherWakes)
	}
	if got := wakeup.Count(); got != 1 {
		t.Errorf("promotion online runtime hints=%d want 1 after commit", got)
	}
	// Same-role request retry is an idempotent 200 but must not duplicate
	// either the durable role event or the directed wake.
	patchChannelMemberRole(t, fixture.channel.ID, "agent", fixture.agentA, "manager")
	count, _, _ = roleChangeSystemEvents(t, fixture.channel.ID, "agent", fixture.agentA)
	wakes, _, _ = roleChangeWakeCount(t, fixture.channel.ID, fixture.agentA)
	if count != 1 || wakes != 1 {
		t.Errorf("same-role retry changed event/wake counts to %d/%d want 1/1", count, wakes)
	}
	if got := wakeup.Count(); got != 1 {
		t.Errorf("same-role retry changed online runtime hints to %d want 1", got)
	}

	patchChannelMemberRole(t, fixture.channel.ID, "agent", fixture.agentA, "member")
	count, _, _ = roleChangeSystemEvents(t, fixture.channel.ID, "agent", fixture.agentA)
	wakes, minPriority, maxPriority = roleChangeWakeCount(t, fixture.channel.ID, fixture.agentA)
	if count != 2 {
		t.Errorf("promotion+demotion role event count=%d want 2", count)
	}
	if wakes != 2 || minPriority != 10 || maxPriority != 10 {
		t.Errorf(
			"promotion+demotion wakes=(count=%d min=%d max=%d) want (2,%d,%d)",
			wakes, minPriority, maxPriority,
			10, 10,
		)
	}
	if got := wakeup.Count(); got != 2 {
		t.Errorf("promotion+demotion online runtime hints=%d want 2", got)
	}
	if got := roleChangeTransitions(t, fixture.channel.ID, "agent", fixture.agentA); !reflect.DeepEqual(
		got,
		[]roleTransitionContract{
			{Previous: "member", New: "manager"},
			{Previous: "manager", New: "member"},
		},
	) {
		t.Fatalf("ordered role transitions=%+v want member→manager then manager→member", got)
	}
	if reminderAfter := ordinaryRoleContractReminderSnapshot(t, reminderID); reminderAfter != reminderBefore {
		t.Fatalf("role transition mutated ordinary Reminder\nbefore=%s\nafter=%s", reminderBefore, reminderAfter)
	}
	var reminderCount int
	if err := testPool.QueryRow(context.Background(), `
		SELECT count(*) FROM agent_reminder WHERE workspace_id = $1 AND agent_id = $2`,
		testWorkspaceID, fixture.agentA,
	).Scan(&reminderCount); err != nil {
		t.Fatalf("count role-contract Reminders: %v", err)
	}
	if reminderCount != 1 {
		t.Fatalf("role transition manufactured Reminder rows: got=%d want=1", reminderCount)
	}
}

func TestReminderWakeRacingManagerRemovalUsesCurrentRole(t *testing.T) {
	fixture := newRoleWakeFixture(t)
	patchChannelMemberRole(t, fixture.channel.ID, "agent", fixture.agentA, "manager")

	var eventID pgtype.UUID
	if err := testPool.QueryRow(context.Background(), `
		SELECT id
		FROM agent_inbox_event
		WHERE workspace_id = $1 AND channel_id = $2 AND agent_id = $3 AND reason = $4
		ORDER BY created_at, id
		LIMIT 1`,
		testWorkspaceID, fixture.channel.ID, fixture.agentA, channelRoleChangedWakeContractReason,
	).Scan(&eventID); err != nil {
		t.Fatalf("load role wake fixture: %v", err)
	}
	event, err := testHandler.Queries.GetAgentInboxEvent(context.Background(), eventID)
	if err != nil {
		t.Fatalf("get role wake fixture: %v", err)
	}
	contextJSON, err := json.Marshal(channelWakeContext{
		Type: channelWakeContextType, Prompt: "ordinary Reminder due",
		ChannelID: fixture.channel.ID,
	})
	if err != nil {
		t.Fatalf("marshal Reminder wake context: %v", err)
	}
	event.Reason = "reminder"
	event.Context = contextJSON
	event.TriggerSummary = pgtype.Text{String: "ordinary Reminder due", Valid: true}

	agentRow, err := testHandler.Queries.GetAgent(context.Background(), parseUUID(fixture.agentA))
	if err != nil {
		t.Fatalf("load Reminder owner: %v", err)
	}
	runtime, err := testHandler.Queries.GetAgentRuntime(context.Background(), agentRow.RuntimeID)
	if err != nil {
		t.Fatalf("load Reminder owner runtime: %v", err)
	}
	// The Reminder was queued while the role existed, then removal committed
	// before claim. Claim must rebuild authority from the durable roster.
	patchChannelMemberRole(t, fixture.channel.ID, "agent", fixture.agentA, "member")
	task := testHandler.agentInboxTaskResponse(
		context.Background(),
		runtime,
		event,
		db.AgentEventDelivery{
			ID: parseUUID(uuid.NewString()), InboxEventID: event.ID,
			AgentSessionID: event.AgentSessionID, RuntimeID: runtime.ID,
			LeaseToken: parseUUID(uuid.NewString()),
		},
	)
	if task == nil || task.Agent == nil {
		t.Fatal("Reminder race claim did not produce Agent context")
	}
	if got := taskAgentManagerChannelsForContract(t, task.Agent); len(got) != 0 {
		t.Fatalf("Reminder race retained stale manager authority: %+v", got)
	}
}

func TestHumanManagerRoleTransitionWritesEventWithoutAgentWake(t *testing.T) {
	fixture := newRoleWakeFixture(t)
	humanID := createChannelPlainMember(t)
	if _, err := testPool.Exec(context.Background(), `
		INSERT INTO channel_member (
			channel_id, workspace_id, member_type, member_id, role
		)
		VALUES ($1, $2, 'user', $3, 'member')`,
		fixture.channel.ID, testWorkspaceID, humanID,
	); err != nil {
		t.Fatalf("seed human member: %v", err)
	}

	patchChannelMemberRole(t, fixture.channel.ID, "user", humanID, "manager")
	count, previousRole, newRole := roleChangeSystemEvents(
		t, fixture.channel.ID, "human", humanID,
	)
	if count != 1 || previousRole != "member" || newRole != "manager" {
		t.Errorf(
			"human promotion events=(count=%d previous=%q new=%q) want (1,member,manager)",
			count, previousRole, newRole,
		)
	}
	var totalRoleWakes int
	if err := testPool.QueryRow(context.Background(), `
		SELECT count(*)
		FROM agent_inbox_event
		WHERE workspace_id = $1
		  AND channel_id = $2
		  AND reason = $3`,
		testWorkspaceID,
		fixture.channel.ID,
		channelRoleChangedWakeContractReason,
	).Scan(&totalRoleWakes); err != nil {
		t.Fatalf("count human-target role wakes: %v", err)
	}
	if totalRoleWakes != 0 {
		t.Errorf("human role change created %d agent wakes want 0", totalRoleWakes)
	}
}

func taskAgentManagerChannelsForContract(
	t *testing.T,
	agent *TaskAgentData,
) []managerChannelContract {
	t.Helper()
	if agent == nil {
		t.Fatal("claim task agent is nil")
	}
	field := reflect.ValueOf(agent).Elem().FieldByName("ManagerChannels")
	if !field.IsValid() {
		t.Fatal("TaskAgentData.ManagerChannels production field is not implemented")
	}
	if field.Kind() != reflect.Slice {
		t.Fatalf("TaskAgentData.ManagerChannels kind=%s want slice", field.Kind())
	}
	out := make([]managerChannelContract, 0, field.Len())
	for i := 0; i < field.Len(); i++ {
		item := field.Index(i)
		if item.Kind() == reflect.Pointer {
			if item.IsNil() {
				t.Fatalf("ManagerChannels[%d] is nil", i)
			}
			item = item.Elem()
		}
		if item.Kind() != reflect.Struct {
			t.Fatalf("ManagerChannels[%d] kind=%s want struct", i, item.Kind())
		}
		id := item.FieldByName("ID")
		name := item.FieldByName("Name")
		if !id.IsValid() || id.Kind() != reflect.String ||
			!name.IsValid() || name.Kind() != reflect.String {
			t.Fatalf("ManagerChannels[%d] must expose string ID and Name", i)
		}
		out = append(out, managerChannelContract{ID: id.String(), Name: name.String()})
	}
	return out
}

func createRoleWakeExtraChannel(
	t *testing.T,
	name, agentID string,
	archived bool,
) ChannelResponse {
	t.Helper()
	req := newRequestAs(testUserID, http.MethodPost, "/api/channels", map[string]any{"name": name})
	req = withChannelTestWorkspaceCtx(t, req, testUserID)
	rec := httptest.NewRecorder()
	testHandler.CreateChannel(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create extra channel status=%d body=%s", rec.Code, rec.Body.String())
	}
	var channel ChannelResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &channel); err != nil {
		t.Fatalf("decode extra channel: %v", err)
	}
	if _, err := testPool.Exec(context.Background(), `
		INSERT INTO channel_member (
			channel_id, workspace_id, member_type, member_id, role
		)
		VALUES ($1, $2, 'agent', $3, 'manager')`,
		channel.ID, testWorkspaceID, agentID,
	); err != nil {
		t.Fatalf("seed extra manager membership: %v", err)
	}
	if archived {
		if _, err := testPool.Exec(context.Background(), `
			UPDATE channel
			SET archived_at = now(), archived_by = $3
			WHERE id = $1 AND workspace_id = $2`,
			channel.ID, testWorkspaceID, testUserID,
		); err != nil {
			t.Fatalf("archive extra channel: %v", err)
		}
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM channel WHERE id = $1`, channel.ID)
	})
	return channel
}

func TestRoleWakeClaimAggregatesUnarchivedManagerChannelsFromRosterSource(t *testing.T) {
	fixture := newRoleWakeFixture(t)
	patchChannelMemberRole(t, fixture.channel.ID, "agent", fixture.agentA, "manager")

	second := createRoleWakeExtraChannel(
		t,
		"manager-role-second-"+uuid.NewString()[:8],
		fixture.agentA,
		false,
	)
	archived := createRoleWakeExtraChannel(
		t,
		"manager-role-archived-"+uuid.NewString()[:8],
		fixture.agentA,
		true,
	)

	// Simulate the env-dispatch execution identity: the durable channel roster
	// stays on agentA, while the claimed event executes as a derived agent.
	derivedAgentID := createHandlerTestAgent(t, "manager-role-derived-"+uuid.NewString(), nil)
	if _, err := testPool.Exec(context.Background(), `
		UPDATE agent
		SET source_agent_id = $2
		WHERE id = $1 AND workspace_id = $3`,
		derivedAgentID, fixture.agentA, testWorkspaceID,
	); err != nil {
		t.Fatalf("link derived agent to roster source: %v", err)
	}

	var eventID pgtype.UUID
	if err := testPool.QueryRow(context.Background(), `
		UPDATE agent_inbox_event
		SET agent_id = $3
		WHERE id = (
			SELECT id
			FROM agent_inbox_event
			WHERE workspace_id = $1
			  AND channel_id = $2
			  AND reason = $4
			ORDER BY created_at, id
			LIMIT 1
		)
		RETURNING id`,
		testWorkspaceID,
		fixture.channel.ID,
		derivedAgentID,
		channelRoleChangedWakeContractReason,
	).Scan(&eventID); err != nil {
		t.Fatalf("rewrite role wake to derived execution identity: %v", err)
	}
	event, err := testHandler.Queries.GetAgentInboxEvent(context.Background(), eventID)
	if err != nil {
		t.Fatalf("load role wake: %v", err)
	}
	derivedAgent, err := testHandler.Queries.GetAgent(context.Background(), parseUUID(derivedAgentID))
	if err != nil {
		t.Fatalf("load derived agent: %v", err)
	}
	runtime, err := testHandler.Queries.GetAgentRuntime(context.Background(), derivedAgent.RuntimeID)
	if err != nil {
		t.Fatalf("load role wake runtime: %v", err)
	}
	// Avoid transport-token persistence; this test exercises the claim read
	// model and does not need a real leased delivery row.
	task := testHandler.agentInboxTaskResponse(
		context.Background(),
		runtime,
		event,
		db.AgentEventDelivery{
			ID:             parseUUID(uuid.NewString()),
			InboxEventID:   event.ID,
			AgentSessionID: event.AgentSessionID,
			RuntimeID:      runtime.ID,
			LeaseToken:     parseUUID(uuid.NewString()),
			LeaseExpiresAt: pgtype.Timestamptz{},
		},
	)
	if task == nil {
		t.Fatal("role wake claim task is nil")
	}

	got := taskAgentManagerChannelsForContract(t, task.Agent)
	sort.Slice(got, func(i, j int) bool {
		if got[i].Name == got[j].Name {
			return got[i].ID < got[j].ID
		}
		return got[i].Name < got[j].Name
	})
	want := []managerChannelContract{
		{ID: fixture.channel.ID, Name: fixture.channel.Name},
		{ID: second.ID, Name: second.Name},
	}
	sort.Slice(want, func(i, j int) bool {
		if want[i].Name == want[j].Name {
			return want[i].ID < want[j].ID
		}
		return want[i].Name < want[j].Name
	})
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("derived claim manager channels=%+v want active roster-source channels %+v", got, want)
	}
	// A new Handler reconstructs responsibility solely from durable roster
	// state; no process-local role/reminder cache participates in recovery.
	recoveredHandler := &Handler{DB: testPool}
	recoveredRows := recoveredHandler.agentManagerChannels(
		context.Background(),
		parseUUID(testWorkspaceID),
		parseUUID(derivedAgentID),
	)
	recovered := make([]managerChannelContract, 0, len(recoveredRows))
	for _, channel := range recoveredRows {
		recovered = append(recovered, managerChannelContract{ID: channel.ID, Name: channel.Name})
	}
	sort.Slice(recovered, func(i, j int) bool {
		if recovered[i].Name == recovered[j].Name {
			return recovered[i].ID < recovered[j].ID
		}
		return recovered[i].Name < recovered[j].Name
	})
	if !reflect.DeepEqual(recovered, got) {
		t.Fatalf("recovered manager responsibility=%+v want %+v", recovered, got)
	}
	for _, channel := range got {
		if channel.ID == archived.ID {
			t.Fatalf("archived manager channel leaked into claim: %+v", channel)
		}
	}

	// The manager brief is rebuilt from this claim read model on every run.
	// Demotion must therefore remove the channel immediately, without a
	// separate cleanup path or stale role text surviving in the next brief.
	patchChannelMemberRole(t, fixture.channel.ID, "agent", fixture.agentA, "member")
	afterDemotion := testHandler.agentManagerChannels(
		context.Background(),
		parseUUID(testWorkspaceID),
		parseUUID(derivedAgentID),
	)
	if len(afterDemotion) != 1 ||
		afterDemotion[0].ID != second.ID ||
		afterDemotion[0].Name != second.Name {
		t.Fatalf(
			"manager channels after demotion=%+v want only remaining active manager channel %+v",
			afterDemotion,
			second,
		)
	}
}
