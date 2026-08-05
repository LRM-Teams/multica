package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestMigration202BackfillsOnlyProvenExplicitUnfollow(t *testing.T) {
	if testPool == nil {
		t.Skip("database not available")
	}
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve current test file")
	}
	migration, err := os.ReadFile(filepath.Join(filepath.Dir(thisFile), "..", "..", "migrations", "202_thread_participant_explicit_unfollow.up.sql"))
	if err != nil {
		t.Fatalf("read migration 202: %v", err)
	}

	ctx := context.Background()
	tx, err := testPool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin migration test transaction: %v", err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `
		CREATE TEMP TABLE thread_participant (
		  root_message_id UUID NOT NULL,
		  member_type TEXT NOT NULL,
		  member_id UUID NOT NULL,
		  followed_at TIMESTAMPTZ,
		  wake_state TEXT NOT NULL DEFAULT 'active' CHECK (wake_state IN ('active', 'no_wake', 'removed')),
		  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
		  PRIMARY KEY (root_message_id, member_type, member_id)
		) ON COMMIT DROP;
		CREATE TEMP TABLE channel_thread_state (
		  root_message_id UUID NOT NULL,
		  user_id UUID NOT NULL,
		  followed_at TIMESTAMPTZ
		) ON COMMIT DROP;
		CREATE TEMP TABLE channel_message (
		  id UUID PRIMARY KEY,
		  thread_root_message_id UUID,
		  author_type TEXT NOT NULL,
		  parts JSONB NOT NULL DEFAULT '[]'::jsonb
		) ON COMMIT DROP;
		CREATE TEMP TABLE agent_task_transport_audit (
		  action TEXT NOT NULL,
		  channel_message_id UUID,
		  agent_id UUID NOT NULL
		) ON COMMIT DROP;`); err != nil {
		t.Fatalf("create legacy migration fixtures: %v", err)
	}

	type fixture struct {
		label         string
		memberType    string
		followed      bool
		wakeState     string
		humanStateRow bool
		humanFollowed bool
		auditEvidence bool
		wantState     string
	}
	fixtures := []fixture{
		{label: "human explicit from active legacy row", memberType: "user", wakeState: "active", humanStateRow: true, wantState: "unfollowed"},
		{label: "human explicit from no wake legacy row", memberType: "user", wakeState: "no_wake", humanStateRow: true, wantState: "unfollowed"},
		{label: "human active follow", memberType: "user", followed: true, wakeState: "active", humanStateRow: true, humanFollowed: true, wantState: "active"},
		{label: "human no state evidence", memberType: "user", wakeState: "no_wake", wantState: "no_wake"},
		{label: "human removed", memberType: "user", wakeState: "removed", humanStateRow: true, wantState: "removed"},
		{label: "agent directed no wake", memberType: "agent", wakeState: "no_wake", wantState: "no_wake"},
		{label: "agent audit explicit unfollow", memberType: "agent", wakeState: "no_wake", auditEvidence: true, wantState: "unfollowed"},
	}
	fixturesByRoot := make(map[string]fixture, len(fixtures))
	for _, fixture := range fixtures {
		rootID, memberID := uuid.NewString(), uuid.NewString()
		var followedAt any
		if fixture.followed {
			followedAt = "2026-07-20T00:00:00Z"
		}
		if _, err := tx.Exec(ctx, `INSERT INTO thread_participant (root_message_id, member_type, member_id, followed_at, wake_state) VALUES ($1,$2,$3,$4,$5)`, rootID, fixture.memberType, memberID, followedAt, fixture.wakeState); err != nil {
			t.Fatalf("insert %s participant: %v", fixture.label, err)
		}
		if fixture.humanStateRow {
			var humanFollowedAt any
			if fixture.humanFollowed {
				humanFollowedAt = "2026-07-20T00:00:00Z"
			}
			if _, err := tx.Exec(ctx, `INSERT INTO channel_thread_state (root_message_id, user_id, followed_at) VALUES ($1,$2,$3)`, rootID, memberID, humanFollowedAt); err != nil {
				t.Fatalf("insert %s human state: %v", fixture.label, err)
			}
		}
		if fixture.auditEvidence {
			if _, err := tx.Exec(ctx, `INSERT INTO agent_task_transport_audit (action, channel_message_id, agent_id) VALUES ('thread_unfollow',$1,$2)`, rootID, memberID); err != nil {
				t.Fatalf("insert %s audit: %v", fixture.label, err)
			}
		}
		fixture.label = rootID + ":" + fixture.label
		fixturesByRoot[rootID] = fixture
	}

	if _, err := tx.Exec(ctx, string(migration)); err != nil {
		t.Fatalf("apply migration 202: %v", err)
	}
	for rootID, fixture := range fixturesByRoot {
		var got string
		if err := tx.QueryRow(ctx, `SELECT wake_state FROM thread_participant WHERE root_message_id = $1`, rootID).Scan(&got); err != nil {
			t.Fatalf("read %s migrated state: %v", fixture.label, err)
		}
		if got != fixture.wantState {
			t.Fatalf("%s wake_state=%q, want %q", fixture.label, got, fixture.wantState)
		}
	}
}

func TestHumanThreadFollowLifecyclePreservesExplicitOptOutInGroupAndDM(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	for _, channelKind := range []string{"group", "dm"} {
		t.Run(channelKind, func(t *testing.T) {
			ctx := context.Background()
			targetHandle := "thread-human-" + uuid.NewString()[:8]
			targetID := createWorkspaceMemberUser(t, targetHandle, targetHandle+"@multica.test")
			channelID := seedChannelForTest(t, "human-follow-"+channelKind+"-"+uuid.NewString(), testUserID, targetID)
			if channelKind == "dm" {
				if _, err := testPool.Exec(ctx, `UPDATE channel SET kind = 'dm' WHERE id = $1`, channelID); err != nil {
					t.Fatalf("mark channel as dm: %v", err)
				}
			}

			sendReply := func(authorID string, root ChannelMessageResponse, content string, parts ...protocol.MessagePart) ChannelMessageResponse {
				t.Helper()
				req := newRequestAs(authorID, http.MethodPost, "/api/channels/"+channelID+"/messages/"+root.ID+"/thread", map[string]any{
					"content": content,
					"parts":   parts,
				})
				req = withChannelTestWorkspaceCtx(t, req, authorID)
				req = withURLParams(req, "channelId", channelID, "messageId", root.ID)
				rec := httptest.NewRecorder()
				testHandler.SendChannelMessageThreadReply(rec, req)
				if rec.Code != http.StatusCreated {
					t.Fatalf("send %s thread reply: status=%d body=%s", channelKind, rec.Code, rec.Body.String())
				}
				var reply ChannelMessageResponse
				if err := json.Unmarshal(rec.Body.Bytes(), &reply); err != nil {
					t.Fatalf("decode thread reply: %v", err)
				}
				return reply
			}
			setFollow := func(root ChannelMessageResponse, followed bool) {
				t.Helper()
				method := http.MethodPost
				if !followed {
					method = http.MethodDelete
				}
				req := newRequestAs(targetID, method, "/api/channels/"+channelID+"/messages/"+root.ID+"/thread/follow", nil)
				req = withChannelTestWorkspaceCtx(t, req, targetID)
				req = withURLParams(req, "channelId", channelID, "messageId", root.ID)
				rec := httptest.NewRecorder()
				if followed {
					testHandler.FollowChannelThread(rec, req)
				} else {
					testHandler.UnfollowChannelThread(rec, req)
				}
				if rec.Code != http.StatusOK {
					t.Fatalf("set thread followed=%v: status=%d body=%s", followed, rec.Code, rec.Body.String())
				}
			}
			markRead := func(root ChannelMessageResponse) {
				t.Helper()
				req := newRequestAs(targetID, http.MethodPost, "/api/channels/"+channelID+"/messages/"+root.ID+"/thread/read", nil)
				req = withChannelTestWorkspaceCtx(t, req, targetID)
				req = withURLParams(req, "channelId", channelID, "messageId", root.ID)
				rec := httptest.NewRecorder()
				testHandler.MarkChannelThreadRead(rec, req)
				if rec.Code != http.StatusOK {
					t.Fatalf("mark thread read: status=%d body=%s", rec.Code, rec.Body.String())
				}
			}
			assertState := func(root ChannelMessageResponse, wantFollowed bool, wantWakeState string) {
				t.Helper()
				var canonicalFollowed, legacyFollowed bool
				var wakeState string
				if err := testPool.QueryRow(ctx, `
					SELECT participant.followed_at IS NOT NULL, state.followed_at IS NOT NULL, participant.wake_state
					FROM thread_participant participant
					JOIN channel_thread_state state
					  ON state.root_message_id = participant.root_message_id
					 AND state.user_id = participant.member_id
					WHERE participant.root_message_id = $1
					  AND participant.member_type = 'user'
					  AND participant.member_id = $2`, root.ID, targetID).Scan(&canonicalFollowed, &legacyFollowed, &wakeState); err != nil {
					t.Fatalf("load human thread state: %v", err)
				}
				if canonicalFollowed != wantFollowed || legacyFollowed != wantFollowed || wakeState != wantWakeState {
					t.Fatalf("human thread state = canonical:%v legacy:%v wake:%q, want %v/%v/%q", canonicalFollowed, legacyFollowed, wakeState, wantFollowed, wantFollowed, wantWakeState)
				}
				messages := listedMessagesForUser(t, channelID, targetID)
				for _, message := range messages {
					if message.ID == root.ID {
						if message.ThreadFollowed != wantFollowed {
							t.Fatalf("thread_followed=%v, want %v", message.ThreadFollowed, wantFollowed)
						}
						if !wantFollowed && message.ThreadUnreadCount != 0 {
							t.Fatalf("unfollowed thread unread_count=%d, want 0", message.ThreadUnreadCount)
						}
						return
					}
				}
				t.Fatalf("thread root %s missing from target read model", root.ID)
			}
			mention := protocol.MessagePart{
				Type:       protocol.MessagePartTypeReference,
				RefType:    "mention",
				RefSubType: "member",
				RefID:      targetID,
				Label:      "@" + targetHandle,
			}

			directOptOutRoot, err := testHandler.insertChannelMessage(ctx, parseUUID(channelID), parseUUID(testWorkspaceID), "user", parseUUID(testUserID), "Tester", "direct opt out before participation "+uuid.NewString(), "multica", nil, pgtype.UUID{}, pgtype.UUID{}, nil, 0)
			if err != nil {
				t.Fatalf("insert direct opt-out root: %v", err)
			}
			setFollow(directOptOutRoot, false)
			setFollow(directOptOutRoot, false)
			assertState(directOptOutRoot, false, "unfollowed")
			sendReply(testUserID, directOptOutRoot, "ordinary reply after direct opt out")
			assertState(directOptOutRoot, false, "unfollowed")
			sendReply(testUserID, directOptOutRoot, "@"+targetHandle+" mention after direct opt out", mention)
			assertState(directOptOutRoot, false, "unfollowed")

			rootByTarget, err := testHandler.insertChannelMessage(ctx, parseUUID(channelID), parseUUID(testWorkspaceID), "user", parseUUID(targetID), targetHandle, "root author implicit follow "+uuid.NewString(), "multica", nil, pgtype.UUID{}, pgtype.UUID{}, nil, 0)
			if err != nil {
				t.Fatalf("insert target-authored root: %v", err)
			}
			sendReply(testUserID, rootByTarget, "first ordinary reply follows root author")
			assertState(rootByTarget, true, "active")
			setFollow(rootByTarget, false)
			assertState(rootByTarget, false, "unfollowed")
			sendReply(testUserID, rootByTarget, "ordinary reply after root-author unfollow")
			assertState(rootByTarget, false, "unfollowed")

			rootByOther, err := testHandler.insertChannelMessage(ctx, parseUUID(channelID), parseUUID(testWorkspaceID), "user", parseUUID(testUserID), "Tester", "mention implicit follow "+uuid.NewString(), "multica", nil, pgtype.UUID{}, pgtype.UUID{}, nil, 0)
			if err != nil {
				t.Fatalf("insert other-authored root: %v", err)
			}
			markRead(rootByOther)
			assertState(rootByOther, false, "no_wake")
			sendReply(testUserID, rootByOther, "@"+targetHandle+" first personal mention", mention)
			if channelKind == "dm" {
				// DMs deliberately keep actor mentions as plain text. Only group
				// messages resolve them into member delivery/follow state.
				assertState(rootByOther, false, "no_wake")
				setFollow(rootByOther, true)
			}
			assertState(rootByOther, true, "active")
			setFollow(rootByOther, false)
			assertState(rootByOther, false, "unfollowed")
			markRead(rootByOther)
			assertState(rootByOther, false, "unfollowed")
			mentionedAfterUnfollow := sendReply(testUserID, rootByOther, "@"+targetHandle+" mention after explicit unfollow", mention)
			assertState(rootByOther, false, "unfollowed")
			var mentionInboxCount int
			if err := testPool.QueryRow(ctx, `
				SELECT count(*)
				FROM inbox_item
				WHERE recipient_id = $1
				  AND type = 'mentioned'
				  AND details->>'message_id' = $2`, targetID, mentionedAfterUnfollow.ID).Scan(&mentionInboxCount); err != nil {
				t.Fatalf("count mention-after-unfollow inbox delivery: %v", err)
			}
			wantMentionInboxCount := 1
			if channelKind == "dm" {
				wantMentionInboxCount = 0
			}
			if mentionInboxCount != wantMentionInboxCount {
				t.Fatalf("mention-after-unfollow inbox deliveries=%d, want %d", mentionInboxCount, wantMentionInboxCount)
			}
			sendReply(targetID, rootByOther, "my own post re-follows")
			assertState(rootByOther, true, "active")
			setFollow(rootByOther, false)
			assertState(rootByOther, false, "unfollowed")
			setFollow(rootByOther, true)
			assertState(rootByOther, true, "active")
		})
	}
}
