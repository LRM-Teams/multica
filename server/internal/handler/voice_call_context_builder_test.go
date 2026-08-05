package handler

import (
	"context"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/service/voicecall"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func TestVoiceCallWelcomeMessageDefaultsUnsetMemberLanguageToChinese(t *testing.T) {
	member := db.User{DisplayName: "调用者"}
	agent := db.Agent{DisplayName: "贝克汉姆"}

	got := voiceCallWelcomeMessage(member, agent)
	want := "你好，调用者。我是贝克汉姆。你想聊什么？"
	if got != want {
		t.Fatalf("welcome message = %q, want %q", got, want)
	}

	behavior := voiceCallBehaviorBody(member)
	for _, wantInstruction := range []string{
		"Speak in Simplified Chinese throughout the call.",
		"Do not switch to English unless the member explicitly asks you to.",
	} {
		if !strings.Contains(behavior, wantInstruction) {
			t.Fatalf("voice call behavior missing %q: %q", wantInstruction, behavior)
		}
	}
}

func TestVoiceCallKeepsExplicitEnglishMemberLanguage(t *testing.T) {
	member := db.User{
		DisplayName: "Caller",
		Language:    pgtype.Text{String: "en-US", Valid: true},
	}
	agent := db.Agent{DisplayName: "Beckham"}

	got := voiceCallWelcomeMessage(member, agent)
	want := "Hello, Caller. This is Beckham. What would you like to discuss?"
	if got != want {
		t.Fatalf("welcome message = %q, want %q", got, want)
	}
	if strings.Contains(voiceCallBehaviorBody(member), "Simplified Chinese") {
		t.Fatalf("explicit English preference received Chinese voice behavior")
	}
}

func TestVoiceCallContextBuilderUsesCanonicalIdentityAndBoundedProjectContext(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	agentID := createHandlerTestAgent(t, "贝克汉姆通话测试", []byte("[]"))
	dmChannelID := seedAgentDMChannel(t, agentID)
	relatedChannelID := seedChannelForTest(t, "语音发布协调-"+uuid.NewString()[:8], testUserID)
	unrelatedChannelID := seedChannelForTest(t, "语音无权群-"+uuid.NewString()[:8], testUserID)
	if _, err := testPool.Exec(ctx, `
		INSERT INTO channel_member (channel_id, workspace_id, member_type, member_id, role)
		VALUES ($1, $2, 'agent', $3, 'manager')`, relatedChannelID, testWorkspaceID, agentID); err != nil {
		t.Fatalf("join voice call related channel: %v", err)
	}

	var projectID, otherProjectID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO project (workspace_id, title, description, status, priority)
		VALUES ($1, '语音项目', '当前项目说明', 'in_progress', 'high')
		RETURNING id`,
		testWorkspaceID,
	).Scan(&projectID); err != nil {
		t.Fatalf("create voice call project: %v", err)
	}
	if err := testPool.QueryRow(ctx, `
		INSERT INTO project (workspace_id, title, description, status, priority)
		VALUES ($1, '其他项目', '不应进入上下文', 'in_progress', 'medium')
		RETURNING id`,
		testWorkspaceID,
	).Scan(&otherProjectID); err != nil {
		t.Fatalf("create unrelated project: %v", err)
	}
	if _, err := testPool.Exec(
		ctx,
		`UPDATE channel SET project_id = $2 WHERE id = $1`,
		dmChannelID,
		projectID,
	); err != nil {
		t.Fatalf("bind voice call project: %v", err)
	}
	if _, err := testPool.Exec(ctx, `
		UPDATE agent
		SET description = '规格驱动的群管理',
		    instructions = '保持贝克汉姆身份；审核、拆解、派活、催办。'
		WHERE id = $1`,
		agentID,
	); err != nil {
		t.Fatalf("set agent identity: %v", err)
	}

	var activeIssueID, unrelatedIssueID string
	if err := testPool.QueryRow(ctx, `
		WITH bumped AS (
			UPDATE workspace
			SET issue_counter = issue_counter + 1
			WHERE id = $1
			RETURNING issue_counter
		)
		INSERT INTO issue (
			workspace_id, title, description, status, priority,
			creator_type, creator_id, project_id, number
		)
		SELECT $1, '修复通话延迟', '', 'in_progress', 'urgent', 'member', $2, $3, issue_counter
		FROM bumped
		RETURNING id`,
		testWorkspaceID,
		testUserID,
		projectID,
	).Scan(&activeIssueID); err != nil {
		t.Fatalf("create active project issue: %v", err)
	}
	if err := testPool.QueryRow(ctx, `
		WITH bumped AS (
			UPDATE workspace
			SET issue_counter = issue_counter + 1
			WHERE id = $1
			RETURNING issue_counter
		)
		INSERT INTO issue (
			workspace_id, title, description, status, priority,
			creator_type, creator_id, project_id, number
		)
		SELECT $1, '其他项目机密事项', '', 'todo', 'high', 'member', $2, $3, issue_counter
		FROM bumped
		RETURNING id`,
		testWorkspaceID,
		testUserID,
		otherProjectID,
	).Scan(&unrelatedIssueID); err != nil {
		t.Fatalf("create unrelated project issue: %v", err)
	}

	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM channel WHERE id = $1`, dmChannelID)
		testPool.Exec(context.Background(), `DELETE FROM issue WHERE id = ANY($1::uuid[])`, []string{
			activeIssueID,
			unrelatedIssueID,
		})
		testPool.Exec(context.Background(), `DELETE FROM project WHERE id = ANY($1::uuid[])`, []string{
			projectID,
			otherProjectID,
		})
	})

	if _, err := testPool.Exec(
		ctx,
		`UPDATE workspace SET context = '工作区要求：所有结论给出依据。' WHERE id = $1`,
		testWorkspaceID,
	); err != nil {
		t.Fatalf("set workspace context: %v", err)
	}
	if _, err := testPool.Exec(ctx, `
		UPDATE "user"
		SET profile_description = '偏好直接结论和可执行下一步',
		    language = 'zh-CN',
		    timezone = 'Asia/Shanghai'
		WHERE id = $1`,
		testUserID,
	); err != nil {
		t.Fatalf("set member context: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `UPDATE workspace SET context = NULL WHERE id = $1`, testWorkspaceID)
		testPool.Exec(context.Background(), `
			UPDATE "user"
			SET profile_description = '', language = NULL, timezone = NULL
			WHERE id = $1`,
			testUserID,
		)
	})

	if _, err := testPool.Exec(ctx, `
		INSERT INTO agent_memory (
			workspace_id, agent_id, name, content, config, sync_key, created_by
		)
		VALUES (
			$1, $2, '沟通偏好', '先确认事实，再承诺结果。',
			jsonb_build_object(
				'scope', 'user',
				'subject', jsonb_build_object('type', 'member', 'id', $3::text),
				'applies', jsonb_build_object('project_id', $4::text)
			),
			$5, $3::uuid
		)`,
		testWorkspaceID,
		agentID,
		testUserID,
		projectID,
		"voice-call-memory-"+uuid.NewString(),
	); err != nil {
		t.Fatalf("create applicable memory: %v", err)
	}

	if _, err := testHandler.insertChannelMessage(
		ctx,
		parseUUID(dmChannelID),
		parseUUID(testWorkspaceID),
		"user",
		parseUUID(testUserID),
		"调用者",
		"上一轮我问了项目进度",
		"multica",
		nil,
		pgtype.UUID{},
		pgtype.UUID{},
		nil,
		0,
	); err != nil {
		t.Fatalf("insert member DM context: %v", err)
	}
	if _, err := testHandler.insertChannelMessage(
		ctx,
		parseUUID(dmChannelID),
		parseUUID(testWorkspaceID),
		"agent",
		parseUUID(agentID),
		"贝克汉姆通话测试",
		"我会先核对当前 issue",
		"multica",
		nil,
		pgtype.UUID{},
		pgtype.UUID{},
		nil,
		0,
	); err != nil {
		t.Fatalf("insert agent DM context: %v", err)
	}

	builder, err := NewVoiceCallContextBuilder(testHandler)
	if err != nil {
		t.Fatalf("create context builder: %v", err)
	}
	callContext, err := builder.Build(ctx, voicecall.Scope{
		WorkspaceID: testWorkspaceID,
		ChannelID:   dmChannelID,
		AgentID:     agentID,
		UserID:      testUserID,
	})
	if err != nil {
		t.Fatalf("build voice call context: %v", err)
	}

	wantPrefixes := []string{
		"## Agent identity",
		"## Calling member",
		"## Workspace and conversation scope",
		"## Reviewed memory",
		"## Recent DM context",
		"## Current project state",
		"## Related group channels",
		"## Voice conversation behavior",
	}
	if len(callContext.SystemMessages) != len(wantPrefixes) {
		t.Fatalf("system message count = %d, want %d", len(callContext.SystemMessages), len(wantPrefixes))
	}
	for index, prefix := range wantPrefixes {
		if !strings.HasPrefix(callContext.SystemMessages[index], prefix) {
			t.Fatalf("system message %d = %q, want prefix %q", index, callContext.SystemMessages[index], prefix)
		}
	}

	all := strings.Join(callContext.SystemMessages, "\n")
	for _, want := range []string{
		"贝克汉姆通话测试",
		"保持贝克汉姆身份",
		"偏好直接结论和可执行下一步",
		"工作区要求：所有结论给出依据",
		"语音项目",
		"沟通偏好",
		"先确认事实，再承诺结果",
		"上一轮我问了项目进度",
		"我会先核对当前 issue",
		"修复通话延迟",
		"语音发布协调",
		"Speak in Simplified Chinese throughout the call",
		"Never claim that code, files, issues, or external systems changed",
	} {
		if !strings.Contains(all, want) {
			t.Fatalf("voice call context missing %q:\n%s", want, all)
		}
	}
	for _, forbidden := range []string{"其他项目", "其他项目机密事项", "语音无权群", unrelatedChannelID} {
		if strings.Contains(all, forbidden) {
			t.Fatalf("voice call context leaked cross-project text %q:\n%s", forbidden, all)
		}
	}
	if utf8.RuneCountInString(all) > voiceCallContextMaxRunes {
		t.Fatalf("voice call context runes = %d, max %d", utf8.RuneCountInString(all), voiceCallContextMaxRunes)
	}
	if !strings.Contains(callContext.WelcomeMessage, "贝克汉姆通话测试") {
		t.Fatalf("welcome message = %q, want canonical Agent name", callContext.WelcomeMessage)
	}
}

func TestVoiceCallChannelsBodyTreatsMembershipIndexAsRecords(t *testing.T) {
	got := voiceCallChannelsBody([]voiceCallChannelSummary{{
		ID: "channel-1", Name: "发布协调", Role: "manager", ProjectID: "project-1",
	}})
	for _, want := range []string{"discoverable channel records", `"name":"发布协调"`, `"role":"manager"`, "Use the channel context tool"} {
		if !strings.Contains(got, want) {
			t.Fatalf("channel body missing %q: %s", want, got)
		}
	}
}

func TestBoundedVoiceCallSystemMessagePreservesSourceEdges(t *testing.T) {
	body := "start-" + strings.Repeat("中", 200) + "-end"
	got := boundedVoiceCallSystemMessage("Test source", body, 80)
	if utf8.RuneCountInString(got) > 80 {
		t.Fatalf("bounded message runes = %d, want <= 80", utf8.RuneCountInString(got))
	}
	for _, want := range []string{"## Test source", "start-", "-end", voiceCallContextTruncationMarker} {
		if !strings.Contains(got, want) {
			t.Fatalf("bounded message missing %q: %q", want, got)
		}
	}
}
