# Env-Dispatch Scratch Channel Routing Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make scratch env-dispatch group messages bypass ordinary channel onboarding, run only on the isolated sandbox-derived agent, and return a terminal task DAG.

**Architecture:** Mark synthetic agent memberships with an explicit `env_dispatch` join source and exclude those inserts at the onboarding trigger boundary. Centralize derived-agent channel session creation in a transactional helper that validates any existing mapping and cleans up a losing concurrent insert. Keep message creation and task/DAG orchestration in the existing service flow.

**Tech Stack:** Go, PostgreSQL migrations and pgx, Multica handler/service tests, Docker deployment through the upstream `dev` branch, Python/httpx live client.

## Global Constraints

- Never expose external model API keys, PATs, daemon tokens, or runtime credentials.
- Preserve the source agent as the stable `channel_member` mention alias; only the derived agent owns the execution session and sandbox runtime.
- Preserve onboarding for `manual`, `system`, and `system_general` memberships.
- Keep readiness matching on workspace, daemon ID, and sandbox instance ID.
- Use red-green-refactor for every production change.
- Preserve unrelated staged `.gitignore` and untracked `mf_cli/__pycache__/` changes.

---

### Task 1: Suppress onboarding for synthetic env-dispatch memberships

**Files:**
- Create: `server/migrations/209_env_dispatch_channel_join_source.up.sql`
- Create: `server/migrations/209_env_dispatch_channel_join_source.down.sql`
- Modify: `server/internal/handler/env_dispatch.go:782-823`
- Test: `server/internal/handler/env_dispatch_channel_store_test.go`

**Interfaces:**
- Consumes: `channel_member.join_source`, `maintain_channel_agent_onboarding()`.
- Produces: `join_source='env_dispatch'`, for which no onboarding trigger fires.

- [ ] **Step 1: Write the failing channel-creation regression test**

Add `TestEnvDispatchAdapter_CreateChannelSuppressesSourceAgentOnboarding` beside the canonical-policy test:

```go
func TestEnvDispatchAdapter_CreateChannelSuppressesSourceAgentOnboarding(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx, _, envID, _, agentID := setupEnvDispatchChannelStoreFixture(t)
	projectID := projectIDForEnvDispatchStoreFixture(t, ctx, envID)
	adapter := &envDispatchDepsAdapter{h: testHandler}

	channelID, err := adapter.CreateEnvDispatchChannel(
		ctx, testWorkspaceID, testUserID, projectID, envID,
		service.MessageRoster{LeaderID: agentID, AgentIDs: []string{agentID}}, nil,
	)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM channel WHERE id = $1`, channelID)
	})

	var joinSource string
	require.NoError(t, testPool.QueryRow(ctx, `
		SELECT join_source FROM channel_member
		WHERE channel_id = $1 AND member_type = 'agent' AND member_id = $2`,
		channelID, agentID).Scan(&joinSource))
	require.Equal(t, "env_dispatch", joinSource)

	for name, query := range map[string]string{
		"onboarding": `SELECT count(*) FROM channel_agent_onboarding WHERE channel_id = $1 AND agent_id = $2`,
		"join message": `SELECT count(*) FROM channel_message message
			JOIN channel_member member
			  ON member.generation_id = message.membership_generation_id
			WHERE message.channel_id = $1 AND member.member_id = $2`,
		"channel session": `SELECT count(*) FROM channel_agent_session WHERE channel_id = $1 AND agent_id = $2`,
	} {
		var count int
		require.NoError(t, testPool.QueryRow(ctx, query, channelID, agentID).Scan(&count), name)
		require.Zero(t, count, name)
	}
}
```

- [ ] **Step 2: Run the test and verify RED**

Run:

```bash
cd multica/server
go test ./internal/handler -run '^TestEnvDispatchAdapter_CreateChannelSuppressesSourceAgentOnboarding$' -count=1
```

Expected: FAIL because the membership is currently `system` and onboarding artifacts are generated.

- [ ] **Step 3: Add the forward and rollback migrations**

Forward migration:

```sql
BEGIN;

ALTER TABLE channel_member
  DROP CONSTRAINT channel_member_join_source_check;
ALTER TABLE channel_member
  ADD CONSTRAINT channel_member_join_source_check
  CHECK (join_source IN ('manual', 'system', 'system_general', 'env_dispatch'));

DROP TRIGGER trg_maintain_channel_agent_onboarding ON channel_member;

CREATE TRIGGER trg_maintain_channel_agent_onboarding_insert
AFTER INSERT ON channel_member
FOR EACH ROW
WHEN (NEW.join_source <> 'env_dispatch')
EXECUTE FUNCTION maintain_channel_agent_onboarding();

CREATE TRIGGER trg_maintain_channel_agent_onboarding_delete
AFTER DELETE ON channel_member
FOR EACH ROW
EXECUTE FUNCTION maintain_channel_agent_onboarding();

COMMIT;
```

Rollback migration:

```sql
BEGIN;

UPDATE channel_member SET join_source = 'system'
WHERE join_source = 'env_dispatch';

ALTER TABLE channel_member
  DROP CONSTRAINT channel_member_join_source_check;
ALTER TABLE channel_member
  ADD CONSTRAINT channel_member_join_source_check
  CHECK (join_source IN ('manual', 'system', 'system_general'));

DROP TRIGGER trg_maintain_channel_agent_onboarding_insert ON channel_member;
DROP TRIGGER trg_maintain_channel_agent_onboarding_delete ON channel_member;

CREATE TRIGGER trg_maintain_channel_agent_onboarding
AFTER INSERT OR DELETE ON channel_member
FOR EACH ROW
EXECUTE FUNCTION maintain_channel_agent_onboarding();

COMMIT;
```

- [ ] **Step 4: Mark env-dispatch agent memberships explicitly**

Change only the agent member insert:

```go
if _, err := tx.Exec(ctx, `
	INSERT INTO channel_member (
		channel_id, workspace_id, member_type, member_id, join_source
	) VALUES ($1, $2, 'agent', $3, 'env_dispatch')`,
	channelID, workspaceID, agentID); err != nil {
	return "", err
}
```

- [ ] **Step 5: Run focused tests and verify GREEN**

```bash
cd multica/server
go test ./internal/handler -run '^(TestEnvDispatchAdapter_CreateChannelSuppressesSourceAgentOnboarding|TestEnvDispatchAdapter_CreateChannelPersistsCanonicalPolicy|TestChannelOnboarding)' -count=1
```

Expected: PASS; ordinary onboarding remains green.

- [ ] **Step 6: Commit Task 1**

```bash
git add server/migrations/209_env_dispatch_channel_join_source.up.sql \
  server/migrations/209_env_dispatch_channel_join_source.down.sql \
  server/internal/handler/env_dispatch.go \
  server/internal/handler/env_dispatch_channel_store_test.go
git commit -m "fix(env-dispatch): suppress synthetic channel onboarding"
```

---

### Task 2: Make derived channel sessions transactional and idempotent

**Files:**
- Create: `server/internal/handler/env_dispatch_channel_session.go`
- Create: `server/internal/handler/env_dispatch_channel_session_test.go`

**Interfaces:**
- Consumes: `Handler.TxStarter`, `chat_session`, `channel_agent_session`.
- Produces: `ensureEnvDispatchChannelSession(context.Context, envDispatchChannelSessionInput) (string, bool, error)`.

- [ ] **Step 1: Write failing tests**

Define:

```go
type envDispatchChannelSessionInput struct {
	WorkspaceID string
	ProjectID   string
	ChannelID   string
	AgentID     string
	CreatorID   string
	RuntimeID   string
}
```

Add these DB-backed tests using existing handler fixtures:

```go
func TestEnsureEnvDispatchChannelSessionCreatesCanonicalSession(t *testing.T)
func TestEnsureEnvDispatchChannelSessionReusesMatchingSession(t *testing.T)
func TestEnsureEnvDispatchChannelSessionRejectsMismatchedRuntime(t *testing.T)
func TestEnsureEnvDispatchChannelSessionConcurrentWinnerLeavesNoOrphan(t *testing.T)
```

The create case asserts exactly one mapping with matching workspace, project, agent, and runtime. Reuse returns the same ID with `created=false`. Mismatch returns `env-dispatch channel session identity mismatch`. Two concurrent calls must converge on one ID and leave one session.

- [ ] **Step 2: Run tests and verify RED**

```bash
cd multica/server
go test ./internal/handler -run '^TestEnsureEnvDispatchChannelSession' -count=1
```

Expected: build FAIL because the helper does not exist.

- [ ] **Step 3: Implement the transactional helper**

Implement `ensureEnvDispatchChannelSession` with this exact behavior:

```go
func (h *Handler) ensureEnvDispatchChannelSession(
	ctx context.Context,
	in envDispatchChannelSessionInput,
) (string, bool, error) {
	if in.WorkspaceID == "" || in.ProjectID == "" || in.ChannelID == "" ||
		in.AgentID == "" || in.CreatorID == "" || in.RuntimeID == "" {
		return "", false, fmt.Errorf("validation_failed: env-dispatch channel session identity is required")
	}
	tx, err := h.TxStarter.Begin(ctx)
	if err != nil {
		return "", false, fmt.Errorf("begin env-dispatch channel session: %w", err)
	}
	defer tx.Rollback(ctx)

	load := func() (string, error) {
		var sessionID, workspaceID, projectID, agentID, runtimeID string
		err := tx.QueryRow(ctx, `
			SELECT session.id::text, session.workspace_id::text,
			       COALESCE(session.project_id::text, ''), session.agent_id::text,
			       COALESCE(session.runtime_id::text, '')
			FROM channel_agent_session binding
			JOIN chat_session session ON session.id = binding.chat_session_id
			WHERE binding.channel_id = $1 AND binding.agent_id = $2`,
			in.ChannelID, in.AgentID,
		).Scan(&sessionID, &workspaceID, &projectID, &agentID, &runtimeID)
		if err != nil {
			return "", err
		}
		if workspaceID != in.WorkspaceID || projectID != in.ProjectID ||
			agentID != in.AgentID || runtimeID != in.RuntimeID {
			return "", fmt.Errorf("env-dispatch channel session identity mismatch")
		}
		return sessionID, nil
	}

	if sessionID, err := load(); err == nil {
		if err := tx.Commit(ctx); err != nil {
			return "", false, fmt.Errorf("commit existing env-dispatch channel session: %w", err)
		}
		return sessionID, false, nil
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return "", false, err
	}

	var candidateID string
	if err := tx.QueryRow(ctx, `
		INSERT INTO chat_session (
			workspace_id, project_id, agent_id, creator_id, title, runtime_id
		) VALUES ($1, $2, $3, $4, 'env-dispatch', $5)
		RETURNING id::text`,
		in.WorkspaceID, in.ProjectID, in.AgentID, in.CreatorID, in.RuntimeID,
	).Scan(&candidateID); err != nil {
		return "", false, fmt.Errorf("create env-dispatch chat session: %w", err)
	}

	tag, err := tx.Exec(ctx, `
		INSERT INTO channel_agent_session (channel_id, agent_id, chat_session_id)
		VALUES ($1, $2, $3)
		ON CONFLICT (channel_id, agent_id) DO NOTHING`,
		in.ChannelID, in.AgentID, candidateID)
	if err != nil {
		return "", false, fmt.Errorf("create env-dispatch channel session: %w", err)
	}
	if tag.RowsAffected() == 0 {
		if _, err := tx.Exec(ctx, `DELETE FROM chat_session WHERE id = $1`, candidateID); err != nil {
			return "", false, fmt.Errorf("delete losing env-dispatch chat session: %w", err)
		}
		winnerID, err := load()
		if err != nil {
			return "", false, fmt.Errorf("load winning env-dispatch channel session: %w", err)
		}
		if err := tx.Commit(ctx); err != nil {
			return "", false, fmt.Errorf("commit winning env-dispatch channel session: %w", err)
		}
		return winnerID, false, nil
	}

	if err := tx.Commit(ctx); err != nil {
		return "", false, fmt.Errorf("commit env-dispatch channel session: %w", err)
	}
	return candidateID, true, nil
}
```

- [ ] **Step 4: Run tests and verify GREEN**

```bash
cd multica/server
go test ./internal/handler -run '^TestEnsureEnvDispatchChannelSession' -count=1
```

Expected: PASS with one canonical session.

- [ ] **Step 5: Commit Task 2**

```bash
git add server/internal/handler/env_dispatch_channel_session.go \
  server/internal/handler/env_dispatch_channel_session_test.go
git commit -m "fix(env-dispatch): canonicalize derived channel sessions"
```

---

### Task 3: Wire provisioning and assert the group-message identity

**Files:**
- Modify: `server/internal/handler/env_dispatch_channel_provision.go:274-300`
- Modify: `server/internal/handler/env_dispatch_channel_provision.go:477-503`
- Test: `server/internal/handler/env_dispatch_channel_session_test.go`
- Test: `server/internal/service/env_dispatch_test.go:2130-2170`

**Interfaces:**
- Consumes: Task 2 session helper.
- Produces: `ProvisionEnvDispatchAgentResult.ChatSessionID` bound to the derived agent and sandbox runtime.

- [ ] **Step 1: Replace scratch and training insert blocks**

Use the helper in both derived-agent paths:

```go
sessionID, sessionCreated, err := h.ensureEnvDispatchChannelSession(ctx, envDispatchChannelSessionInput{
	WorkspaceID: in.WorkspaceID,
	ProjectID:   in.ProjectID,
	ChannelID:   in.ChannelID,
	AgentID:     derivedID,
	CreatorID:   in.UserID,
	RuntimeID:   runtimeRef.ID,
})
if err != nil {
	return cleanup(fmt.Errorf("ensure env-dispatch channel session: %w", err))
}
```

On `markReady` failure, delete the mapping and chat session only when `sessionCreated` is true, then invoke existing derived-agent/sandbox compensation. This wiring is a refactor onto the red-green-tested helper from Task 2; do not change the legacy branch path.

- [ ] **Step 2: Strengthen the service group-message test**

Extend `TestScratchMessageProvisionsOnlyLeader`:

```go
run := f.channelRuns[0]
if run.ChatSessionID != "binding-session-leader" ||
	run.ChannelID != res.Rollouts[0].ChannelID ||
	run.SourceMessageID == "" ||
	run.ProjectID != res.Rollouts[0].ProjectID ||
	run.EnvID != res.Rollouts[0].EnvID ||
	run.SandboxInstanceID != "binding-sandbox-leader" ||
	run.RuntimeID != "rt-1" {
	t.Fatalf("channel run lost sandbox dispatch identity: %+v", run)
}
```

- [ ] **Step 3: Run focused suites and verify GREEN**

```bash
cd multica/server
go test ./internal/handler -run '^(TestEnvDispatchAdapter_CreateChannel|TestEnsureEnvDispatchChannelSession|TestFindOnlineSandboxRuntime)' -count=1
go test ./internal/service -run '^(TestScratchMessageProvisionsOnlyLeader|TestEnvDispatch_GetDagReadiness)' -count=1
```

Expected: PASS.

- [ ] **Step 4: Commit Task 3**

```bash
git add server/internal/handler/env_dispatch_channel_provision.go \
  server/internal/handler/env_dispatch_channel_session_test.go \
  server/internal/service/env_dispatch_test.go
git commit -m "fix(env-dispatch): route scratch sessions to sandbox agents"
```

---

### Task 4: Verify and merge to upstream dev

**Files:**
- Verify all Task 1-3 files.
- Do not include unrelated user changes.

- [ ] **Step 1: Format and inspect**

```bash
cd multica
gofmt -w server/internal/handler/env_dispatch.go \
  server/internal/handler/env_dispatch_channel_session.go \
  server/internal/handler/env_dispatch_channel_session_test.go \
  server/internal/handler/env_dispatch_channel_provision.go \
  server/internal/handler/env_dispatch_channel_store_test.go \
  server/internal/service/env_dispatch_test.go
git diff --check
git status --short
```

Expected: no whitespace errors and no unplanned files.

- [ ] **Step 2: Run focused and package tests**

```bash
cd multica/server
go test ./internal/handler -run 'EnvDispatch|ChannelOnboarding' -count=1
go test ./internal/service -run 'EnvDispatch|ScratchMessage' -count=1
go test ./internal/handler ./internal/service -count=1
```

Expected: exit 0. Record DB-backed skips explicitly.

- [ ] **Step 3: Run repository hooks**

Discover the Multica repository's canonical lint/test command from its README, Makefile, or CI configuration and run it. At minimum retain the fresh Go formatting and package-test evidence above.

- [ ] **Step 4: Review all commits**

```bash
git status --short
git diff dev@{upstream}...HEAD --stat
git diff dev@{upstream}...HEAD -- server/migrations server/internal/handler server/internal/service
```

Expected: no credentials, unrelated changes, or generated artifacts.

- [ ] **Step 5: Integrate and push upstream dev**

Fetch upstream, confirm the remote tracking relationship, integrate new upstream commits without destructive reset, rerun affected tests after conflicts or overlapping changes, and push the verified commits to the authorized upstream `dev` branch.

Expected: push succeeds and automatic deployment starts for the exact pushed revision.

---

### Task 5: Verify deployed sandbox inference and DAG

**Files:**
- Execute: `../customized_areal/tree_search/agents/multica_client.py`
- No source changes expected.

- [ ] **Step 1: Wait for the exact backend revision**

Over SSH, inspect the running `multica-backend-1` image/revision and deployment logs until they identify the pushed commit. Do not infer deployment from elapsed time.

- [ ] **Step 2: Run the live client**

Run the existing isolated client command with the updated `.env`. It must not print API keys. Expected initial evidence includes a created handle, channel ID, project ID, and rollout/root task ID.

- [ ] **Step 3: Verify message and execution identity**

Using authenticated APIs or read-only database queries, assert:

- Exactly one requested user message exists in the new group channel.
- No onboarding row/event exists for the synthetic agent membership.
- The binding is `ready` and points to a derived agent.
- The derived chat session uses the discovered sandbox runtime.
- The root task carries the same channel, project, environment, session, runtime, sandbox instance, and source message.
- Logs show the updated daemon registering with `sandbox_instance_id`.

- [ ] **Step 4: Wait for inference completion**

Poll without exceeding the client deadline. Confirm the derived agent consumes the task inside the sandbox and emits a terminal response or explicit provider failure. Treat provider rejection separately from routing correctness.

- [ ] **Step 5: Verify terminal DAG**

Poll `/api/v1/env-dispatch/channels/{channel_id}/dag`. Expected progression:

```text
202 {"status":"in_progress"}
200 { ... assembled DAG containing the root task/segment result ... }
```

Confirm the DAG root task matches the sandbox task ID and the result corresponds to the agent response.

- [ ] **Step 6: Clean up and report evidence**

Use the env-dispatch channel cleanup endpoint. Verify channel removal and lifecycle cleanup of its sandbox/runtime/derived agent. Report status codes, task ID, terminal status, and DAG summary with credentials redacted.
