package main

import (
	"os"
	"path/filepath"
	"testing"
)

// These fixtures pin the tool's behavior against the exact shapes found in
// the 2026-08-02 manual scan (task #91). Parker's acceptance bar: catch
// every real positive, zero false positives on the tx-reuse exclusion — if
// a future edit to this tool breaks either side, one of these should fail.

func writeFixture(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestRun_DirectAcquireOnSamePoolReceiver_Flagged(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, dir, "x.go", `package x

func (s *Store) HandleHumanRework(ctx context.Context) error {
	rows, err := s.pool.Query(ctx, "SELECT id FROM work_node")
	if err != nil {
		return err
	}
	for rows.Next() {
		var id int
		rows.Scan(&id)
		if _, err := s.pool.Exec(ctx, "UPDATE work_node SET status = 'x' WHERE id = $1", id); err != nil {
			return err
		}
	}
	return nil
}
`)
	findings, err := run([]string{dir})
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 1 {
		t.Fatalf("findings = %d, want 1: %+v", len(findings), findings)
	}
}

func TestRun_DelegatedHelperAcquire_Flagged(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, dir, "x.go", `package x

func loadIDs(ctx context.Context, pool *pgxpool.Pool, agentID string) []string {
	rows, err := pool.Query(ctx, "SELECT id FROM agent WHERE owner_id = $1", agentID)
	_ = err
	var out []string
	for rows.Next() {
		var id string
		rows.Scan(&id)
		out = append(out, id)
	}
	return out
}

func runCycle(ctx context.Context, pool *pgxpool.Pool) error {
	rows, err := pool.Query(ctx, "SELECT profile_id, owner_id FROM memory_curator_profile")
	if err != nil {
		return err
	}
	for rows.Next() {
		var profileID, ownerID string
		rows.Scan(&profileID, &ownerID)
		ids := loadIDs(ctx, pool, ownerID)
		_ = ids
	}
	return nil
}
`)
	findings, err := run([]string{dir})
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 1 {
		t.Fatalf("findings = %d, want 1 (delegated helper case): %+v", len(findings), findings)
	}
}

func TestRun_QueriesMethodCallInLoop_Flagged(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, dir, "x.go", `package x

func (h *Handler) attachNames(ctx context.Context, resps []Resp) {
	rows, err := h.DB.Query(ctx, "SELECT id FROM agent_runtime")
	if err != nil {
		return
	}
	for rows.Next() {
		var id string
		rows.Scan(&id)
		crashRows, _ := h.Queries.ListAgentCrashedSinceByIDs(ctx, []string{id})
		_ = crashRows
	}
}
`)
	findings, err := run([]string{dir})
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 1 {
		t.Fatalf("findings = %d, want 1 (Queries method call case): %+v", len(findings), findings)
	}
}

// TestRun_TxReuse_NotFlagged pins the exclusion the 2026-08-02 manual scan
// applied to agent_channels.go / channel_member_management_capabilities.go /
// service/task.go / service/issue.go: a second query issued on the SAME
// already-checked-out transaction is not a new pool acquire.
func TestRun_TxReuse_NotFlagged(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, dir, "x.go", `package x

func processInTx(ctx context.Context, tx pgx.Tx) error {
	rows, err := tx.Query(ctx, "SELECT id FROM issue WHERE status = $1", "open")
	if err != nil {
		return err
	}
	for rows.Next() {
		var id string
		rows.Scan(&id)
		if _, err := tx.Exec(ctx, "UPDATE issue SET touched = true WHERE id = $1", id); err != nil {
			return err
		}
	}
	return nil
}
`)
	findings, err := run([]string{dir})
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 0 {
		t.Fatalf("findings = %d, want 0 (tx-reuse must not be flagged): %+v", len(findings), findings)
	}
}

// TestRun_CursorClosedBeforeSecondQuery_NotFlagged: draining into memory and
// closing the outer cursor first (the fix shape used in PR #1812) must not
// be flagged.
func TestRun_CursorClosedBeforeSecondQuery_NotFlagged(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, dir, "x.go", `package x

func loadDMs(ctx context.Context, pool *pgxpool.Pool) []string {
	rows, err := pool.Query(ctx, "SELECT id FROM channel WHERE kind = 'dm'")
	if err != nil {
		return nil
	}
	var ids []string
	for rows.Next() {
		var id string
		rows.Scan(&id)
		ids = append(ids, id)
	}
	rows.Close()
	var out []string
	for _, id := range ids {
		participants, _ := pool.Query(ctx, "SELECT agent_id FROM channel_member WHERE channel_id = $1", id)
		_ = participants
		out = append(out, id)
	}
	return out
}
`)
	findings, err := run([]string{dir})
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 0 {
		t.Fatalf("findings = %d, want 0 (second Query happens after the loop, not inside it): %+v", len(findings), findings)
	}
}

// TestRun_PureInMemoryLoopBody_NotFlagged: the overwhelming majority of real
// rows.Next() loops in this codebase just scan into structs with no further
// DB calls — must never be flagged.
func TestRun_PureInMemoryLoopBody_NotFlagged(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, dir, "x.go", `package x

func listNames(ctx context.Context, pool *pgxpool.Pool) []string {
	rows, err := pool.Query(ctx, "SELECT name FROM agent")
	if err != nil {
		return nil
	}
	defer rows.Close()
	var names []string
	for rows.Next() {
		var name string
		rows.Scan(&name)
		names = append(names, formatName(name))
	}
	return names
}

func formatName(name string) string {
	return name
}
`)
	findings, err := run([]string{dir})
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 0 {
		t.Fatalf("findings = %d, want 0 (pure in-memory loop body): %+v", len(findings), findings)
	}
}

// TestFilterKnown_AllowlistedFindingIsNonBlocking pins the task #90
// allowlist mechanism: a finding matching a knownIssues entry must not end
// up in the blocking slice (which drives CI exit status), but must still be
// reported (in known) so it stays visible in logs — this is a task-tracked
// "not blocking yet," not a silent suppression.
func TestFilterKnown_AllowlistedFindingIsNonBlocking(t *testing.T) {
	// Fixture allowlist — not the real knownIssues map. Real entries get
	// deleted when those bugs are fixed; hardcoding them here would make
	// "fixing a known issue" break the checker's own tests.
	allow := map[knownIssueKey]string{
		{file: "internal/fixture/example.go", funcName: "KnownBad"}: "test fixture",
	}
	f := finding{file: "internal/fixture/example.go", funcName: "KnownBad"}
	blocking, known := filterKnownWith([]finding{f}, allow)
	if len(blocking) != 0 {
		t.Fatalf("blocking = %+v, want empty (this exact finding is in the fixture allowlist)", blocking)
	}
	if len(known) != 1 {
		t.Fatalf("known = %+v, want 1 entry (must stay visible, not silently dropped)", known)
	}
}

// TestFilterKnown_UnknownFindingIsBlocking is the inverse: anything not in
// the allowlist must block, including a finding in the SAME file as an
// allowlisted one but a different function — allowlist entries are scoped
// per-function, not per-file, so a new bug introduced next to a known one
// still fails CI.
func TestFilterKnown_UnknownFindingIsBlocking(t *testing.T) {
	allow := map[knownIssueKey]string{
		{file: "internal/fixture/example.go", funcName: "KnownBad"}: "test fixture",
	}
	f := finding{file: "internal/fixture/example.go", funcName: "someOtherFunc"}
	blocking, known := filterKnownWith([]finding{f}, allow)
	if len(blocking) != 1 {
		t.Fatalf("blocking = %+v, want 1 (not in allowlist, must block)", blocking)
	}
	if len(known) != 0 {
		t.Fatalf("known = %+v, want empty", known)
	}
}
