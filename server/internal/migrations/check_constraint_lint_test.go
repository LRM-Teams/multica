package migrations

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// migration268UpSQL and the two down.sql fixtures below are the real content
// of 268_agent_workspace_file_audit.{up,down}.sql (task #95 / PR #1834) —
// migration268DownBroken is what shipped in the PR's first version, before
// review (task #97's own origin story) caught that it narrows event_kind
// and target_kind without remapping existing rows first;
// migration268DownFixed is what Vera's fix looks like. These are the
// acceptance-criteria fixtures Parker specified: "take the unfixed 268 as
// the red sample, the checker must be able to turn it red."
const migration268UpSQL = `
ALTER TABLE agent_activity_event
    DROP CONSTRAINT agent_activity_event_event_kind_check;

ALTER TABLE agent_activity_event
    ADD CONSTRAINT agent_activity_event_event_kind_check
    CHECK (event_kind IN (
        'thinking', 'tool_call', 'tool_output', 'turn_end', 'session_init',
        'compaction_started', 'compaction_finished', 'wake_attempt', 'error',
        'text', 'system', 'transport', 'telemetry', 'blocked', 'custom',
        'workspace_file'
    ));

ALTER TABLE agent_activity_event
    DROP CONSTRAINT agent_activity_event_target_kind_check;

ALTER TABLE agent_activity_event
    ADD CONSTRAINT agent_activity_event_target_kind_check
    CHECK (target_kind IN ('issue', 'dm', 'channel', 'thread', 'agent', 'file', 'none'));
`

const migration268DownBroken = `
ALTER TABLE agent_activity_event
    DROP CONSTRAINT agent_activity_event_target_kind_check;

ALTER TABLE agent_activity_event
    ADD CONSTRAINT agent_activity_event_target_kind_check
    CHECK (target_kind IN ('issue', 'dm', 'channel', 'thread', 'agent', 'none'));

ALTER TABLE agent_activity_event
    DROP CONSTRAINT agent_activity_event_event_kind_check;

ALTER TABLE agent_activity_event
    ADD CONSTRAINT agent_activity_event_event_kind_check
    CHECK (event_kind IN (
        'thinking', 'tool_call', 'tool_output', 'turn_end', 'session_init',
        'compaction_started', 'compaction_finished', 'wake_attempt', 'error',
        'text', 'system', 'transport', 'telemetry', 'blocked', 'custom'
    ));
`

const migration268DownFixed = `
UPDATE agent_activity_event
SET target_kind = 'none', target_slug = ''
WHERE target_kind = 'file';

UPDATE agent_activity_event
SET event_kind = 'custom'
WHERE event_kind = 'workspace_file';

ALTER TABLE agent_activity_event
    DROP CONSTRAINT agent_activity_event_target_kind_check;

ALTER TABLE agent_activity_event
    ADD CONSTRAINT agent_activity_event_target_kind_check
    CHECK (target_kind IN ('issue', 'dm', 'channel', 'thread', 'agent', 'none'));

ALTER TABLE agent_activity_event
    DROP CONSTRAINT agent_activity_event_event_kind_check;

ALTER TABLE agent_activity_event
    ADD CONSTRAINT agent_activity_event_event_kind_check
    CHECK (event_kind IN (
        'thinking', 'tool_call', 'tool_output', 'turn_end', 'session_init',
        'compaction_started', 'compaction_finished', 'wake_attempt', 'error',
        'text', 'system', 'transport', 'telemetry', 'blocked', 'custom'
    ));
`

// migration163DownSQL is the real content of
// 163_agent_activity_narrative.down.sql — the historical precedent Vera's
// fix was modeled on. A real-world positive control: it narrows event_kind
// and correctly remaps first.
const migration163DownSQL = `
ALTER TABLE agent_activity_event
    DROP CONSTRAINT IF EXISTS agent_activity_event_event_kind_check;

UPDATE agent_activity_event
SET event_kind = CASE
    WHEN event_kind = 'error' THEN 'lifecycle'
    WHEN event_kind = 'transport' THEN 'lifecycle'
    ELSE 'platform_decision'
END
WHERE event_kind NOT IN ('lifecycle', 'platform_decision');

ALTER TABLE agent_activity_event
    ADD CONSTRAINT agent_activity_event_event_kind_check
    CHECK (event_kind IN ('lifecycle', 'platform_decision'));
`

const migration163UpSQL = `
ALTER TABLE agent_activity_event
    DROP CONSTRAINT IF EXISTS agent_activity_event_event_kind_check;

UPDATE agent_activity_event
SET event_kind = CASE
    WHEN severity = 'error' THEN 'error'
    WHEN event_type IN ('server_ping_received', 'daemon_liveness_probe_sent', 'probe_timeout_reconnect', 'transport_reconnected') THEN 'transport'
    ELSE 'custom'
END
WHERE event_kind NOT IN (
    'thinking', 'tool_call', 'tool_output', 'turn_end', 'session_init',
    'compaction_started', 'compaction_finished', 'wake_attempt', 'error',
    'text', 'system', 'transport', 'telemetry', 'blocked', 'custom'
);

ALTER TABLE agent_activity_event
    ADD CONSTRAINT agent_activity_event_event_kind_check
    CHECK (event_kind IN (
        'thinking', 'tool_call', 'tool_output', 'turn_end', 'session_init',
        'compaction_started', 'compaction_finished', 'wake_attempt', 'error',
        'text', 'system', 'transport', 'telemetry', 'blocked', 'custom'
    ));
`

// TestCheckDownMigrationNarrowsSafely_ConfirmBroken is Parker's exact
// acceptance criterion: the unfixed 268 down.sql must turn red.
func TestCheckDownMigrationNarrowsSafely_ConfirmBroken(t *testing.T) {
	found := FindUnsafeNarrowings("268_agent_workspace_file_audit", migration268UpSQL, migration268DownBroken)
	if len(found) == 0 {
		t.Fatal("expected the pre-fix 268 down.sql (missing remap UPDATEs) to be flagged, got no problems")
	}
	var constraints []string
	for _, n := range found {
		constraints = append(constraints, n.Constraint)
		if n.String() == "" {
			t.Errorf("UnsafeNarrowing.String() must not be empty for %+v", n)
		}
	}
	joined := strings.Join(constraints, ",")
	if !strings.Contains(joined, "event_kind") {
		t.Errorf("expected a finding for the event_kind constraint, got: %v", constraints)
	}
	if !strings.Contains(joined, "target_kind") {
		t.Errorf("expected a finding for the target_kind constraint, got: %v", constraints)
	}
}

// TestCheckDownMigrationNarrowsSafely_ConfirmFixed is the other half: the
// real fixed version must pass clean.
func TestCheckDownMigrationNarrowsSafely_ConfirmFixed(t *testing.T) {
	problems := FindUnsafeNarrowings("268_agent_workspace_file_audit", migration268UpSQL, migration268DownFixed)
	if len(problems) != 0 {
		t.Fatalf("expected the fixed 268 down.sql to pass clean, got: %v", problems)
	}
}

// TestCheckDownMigrationNarrowsSafely_HistoricalPositiveControl proves the
// checker doesn't just happen to key off literal fixture text from 268 — a
// real, unrelated historical migration (163) that did the remap-before-narrow
// pattern correctly must also pass.
func TestCheckDownMigrationNarrowsSafely_HistoricalPositiveControl(t *testing.T) {
	problems := FindUnsafeNarrowings("163_agent_activity_narrative", migration163UpSQL, migration163DownSQL)
	if len(problems) != 0 {
		t.Fatalf("expected migration 163's down.sql (which does remap before narrowing) to pass clean, got: %v", problems)
	}
}

// TestCheckDownMigrationNarrowsSafely_WideningIsNotFlagged covers the
// opposite direction: a down.sql that ADDS values back (widening, the
// common non-CHECK-narrowing case) must never be flagged — there's no data
// that could violate a wider constraint.
func TestCheckDownMigrationNarrowsSafely_WideningIsNotFlagged(t *testing.T) {
	up := `
ALTER TABLE widget ADD CONSTRAINT widget_status_check
    CHECK (status IN ('active'));
`
	down := `
ALTER TABLE widget ADD CONSTRAINT widget_status_check
    CHECK (status IN ('active', 'archived'));
`
	problems := FindUnsafeNarrowings("fixture_widening", up, down)
	if len(problems) != 0 {
		t.Fatalf("widening down.sql must never be flagged, got: %v", problems)
	}
}

// TestCheckDownMigrationNarrowsSafely_UpdateAfterNarrowingStillFlagged
// guards the ordering requirement itself: an UPDATE that remaps the
// forbidden value but appears AFTER the narrowing ADD CONSTRAINT is useless
// (the ALTER TABLE will already have failed by the time the UPDATE runs) and
// must still be flagged.
func TestCheckDownMigrationNarrowsSafely_UpdateAfterNarrowingStillFlagged(t *testing.T) {
	up := `
ALTER TABLE widget ADD CONSTRAINT widget_status_check
    CHECK (status IN ('active', 'archived'));
`
	down := `
ALTER TABLE widget ADD CONSTRAINT widget_status_check
    CHECK (status IN ('active'));

UPDATE widget SET status = 'active' WHERE status = 'archived';
`
	problems := FindUnsafeNarrowings("fixture_update_too_late", up, down)
	if len(problems) == 0 {
		t.Fatal("an UPDATE positioned after the narrowing ADD CONSTRAINT must still be flagged — it never gets a chance to run")
	}
}

// TestCheckDownMigrationNarrowsSafely_UnrelatedColumnUpdateNotEnough proves
// the checker requires the remap UPDATE to actually touch the narrowed
// column — an UPDATE on the same table but a different column must not
// satisfy the check.
func TestCheckDownMigrationNarrowsSafely_UnrelatedColumnUpdateNotEnough(t *testing.T) {
	up := `
ALTER TABLE widget ADD CONSTRAINT widget_status_check
    CHECK (status IN ('active', 'archived'));
`
	down := `
UPDATE widget SET name = 'renamed' WHERE status = 'archived';

ALTER TABLE widget ADD CONSTRAINT widget_status_check
    CHECK (status IN ('active'));
`
	problems := FindUnsafeNarrowings("fixture_wrong_column", up, down)
	if len(problems) == 0 {
		t.Fatal("an UPDATE that never sets the narrowed column must not satisfy the check")
	}
}

// TestCheckDownMigrationNarrowsSafely_RaiseExceptionGuardSatisfiesCheck is
// task #104: the checker must recognize task #99/#101's conditional
// RAISE EXCEPTION pattern as a valid resolution, on equal footing with a
// remap UPDATE. Both leave no window where the ADD CONSTRAINT can silently
// destroy or corrupt data — one preserves it via remap, the other refuses
// to proceed at all while it exists. This fixture is migration 143's real
// fix, byte-for-byte (task #101 / PR #1850).
func TestCheckDownMigrationNarrowsSafely_RaiseExceptionGuardSatisfiesCheck(t *testing.T) {
	up := `
ALTER TABLE sandbox_job
    DROP CONSTRAINT IF EXISTS sandbox_job_type_check,
    ADD CONSTRAINT sandbox_job_type_check CHECK (type IN ('create', 'stop', 'resume', 'delete', 'reconfigure', 'exec', 'message'));
`
	down := `
DO $$
DECLARE
    affected_count integer;
BEGIN
    SELECT count(*) INTO affected_count FROM sandbox_job WHERE type = 'reconfigure';
    IF affected_count > 0 THEN
        RAISE EXCEPTION 'migration 143 down cannot proceed: % row(s) in sandbox_job have type=''reconfigure''. There is no safe value to remap them to. If you accept permanently losing these job records, run: DELETE FROM sandbox_job WHERE type = ''reconfigure''; -- then re-run this down migration.', affected_count;
    END IF;
END $$;

ALTER TABLE sandbox_job
    DROP CONSTRAINT IF EXISTS sandbox_job_type_check,
    ADD CONSTRAINT sandbox_job_type_check CHECK (type IN ('create', 'stop', 'resume', 'delete', 'exec', 'message'));
`
	found := FindUnsafeNarrowings("fixture_143", up, down)
	if len(found) != 0 {
		t.Fatalf("a preceding conditional RAISE EXCEPTION guard on the narrowed column must satisfy the check, got: %v", found)
	}
}

// TestCheckDownMigrationNarrowsSafely_RaiseExceptionGuardAfterNarrowingStillFlagged
// mirrors the UPDATE-after-narrowing test: a guard positioned AFTER the
// narrowing ADD CONSTRAINT never gets a chance to run — the ALTER TABLE
// fails (or, worse, silently succeeds if nothing else stops it) before
// the guard is reached.
func TestCheckDownMigrationNarrowsSafely_RaiseExceptionGuardAfterNarrowingStillFlagged(t *testing.T) {
	up := `
ALTER TABLE widget ADD CONSTRAINT widget_status_check
    CHECK (status IN ('active', 'archived'));
`
	down := `
ALTER TABLE widget ADD CONSTRAINT widget_status_check
    CHECK (status IN ('active'));

DO $$
DECLARE
    affected_count integer;
BEGIN
    SELECT count(*) INTO affected_count FROM widget WHERE status = 'archived';
    IF affected_count > 0 THEN
        RAISE EXCEPTION 'too late, % rows', affected_count;
    END IF;
END $$;
`
	found := FindUnsafeNarrowings("fixture_guard_too_late", up, down)
	if len(found) == 0 {
		t.Fatal("a RAISE EXCEPTION guard positioned after the narrowing ADD CONSTRAINT must still be flagged — it never gets a chance to run")
	}
}

// TestCheckDownMigrationNarrowsSafely_RaiseExceptionGuardWrongColumnNotEnough
// proves the guard must actually reference the narrowed column — a guard on
// the same table but checking an unrelated column must not satisfy the
// check (mirrors TestCheckDownMigrationNarrowsSafely_UnrelatedColumnUpdateNotEnough
// for the UPDATE case).
func TestCheckDownMigrationNarrowsSafely_RaiseExceptionGuardWrongColumnNotEnough(t *testing.T) {
	up := `
ALTER TABLE widget ADD CONSTRAINT widget_status_check
    CHECK (status IN ('active', 'archived'));
`
	down := `
DO $$
DECLARE
    affected_count integer;
BEGIN
    SELECT count(*) INTO affected_count FROM widget WHERE name = 'archived-widget';
    IF affected_count > 0 THEN
        RAISE EXCEPTION 'wrong column, % rows', affected_count;
    END IF;
END $$;

ALTER TABLE widget ADD CONSTRAINT widget_status_check
    CHECK (status IN ('active'));
`
	found := FindUnsafeNarrowings("fixture_guard_wrong_column", up, down)
	if len(found) == 0 {
		t.Fatal("a guard that checks an unrelated column must not satisfy the check")
	}
}

// TestFilterKnownUnsafeNarrowings_FixtureAllowlist uses a fixture known-map,
// not the real 33-entry knownPreExistingUnsafeNarrowings — hardcoding a real
// entry here would make this test break the day someone fixes that entry
// and removes it from the map, the exact fragility the cursordeadlock
// checker hit earlier the same day (task #90).
func TestFilterKnownUnsafeNarrowings_FixtureAllowlist(t *testing.T) {
	known := map[knownUnsafeNarrowingKey]bool{
		{Migration: "999_fixture_migration", Constraint: "fixture_check"}: true,
	}
	found := []UnsafeNarrowing{
		{Migration: "999_fixture_migration", Constraint: "fixture_check", Table: "widget", Column: "status", Removed: []string{"archived"}},
		{Migration: "1000_fixture_migration_new", Constraint: "other_check", Table: "widget", Column: "status", Removed: []string{"archived"}},
	}

	newFindings, knownFindings := filterKnownUnsafeNarrowings(found, known)
	if len(knownFindings) != 1 || knownFindings[0].Migration != "999_fixture_migration" {
		t.Fatalf("expected the known-map entry to be filtered into knownFindings, got: %+v", knownFindings)
	}
	if len(newFindings) != 1 || newFindings[0].Migration != "1000_fixture_migration_new" {
		t.Fatalf("expected the unlisted migration to surface as a new finding, got: %+v", newFindings)
	}
}

// TestAllMigrations_DownDoesNotNarrowCheckConstraintsUnsafely is the
// standing regression guard: walks every real up/down migration pair in
// server/migrations. Findings matching knownPreExistingUnsafeNarrowings
// (pre-existing debt discovered while building this checker, task #97 —
// fixing them is a separate follow-up, not part of this checker) are
// reported but don't fail the test; anything NEW does. This is both task
// #97's "audit the existing history" ask and the mechanism that keeps any
// future migration honest going forward.
func TestAllMigrations_DownDoesNotNarrowCheckConstraintsUnsafely(t *testing.T) {
	dir, err := ResolveDir()
	if err != nil {
		t.Skipf("migrations directory not found: %v", err)
	}
	upFiles, err := Files("up")
	if err != nil {
		t.Fatalf("list up migrations: %v", err)
	}

	var allFound []UnsafeNarrowing
	for _, upFile := range upFiles {
		version := ExtractVersion(upFile)
		downFile := filepath.Join(dir, version+".down.sql")
		downBytes, err := os.ReadFile(downFile)
		if os.IsNotExist(err) {
			continue // some migrations are intentionally irreversible / have no down.sql
		}
		if err != nil {
			t.Fatalf("read %s: %v", downFile, err)
		}
		upBytes, err := os.ReadFile(upFile)
		if err != nil {
			t.Fatalf("read %s: %v", upFile, err)
		}
		allFound = append(allFound, FindUnsafeNarrowings(version, string(upBytes), string(downBytes))...)
	}

	newFindings, knownFindings := filterKnownUnsafeNarrowings(allFound, knownPreExistingUnsafeNarrowings)
	if len(knownFindings) > 0 {
		t.Logf("%d known pre-existing unsafe narrowing(s) (task #97 backlog, not blocking):", len(knownFindings))
		for _, n := range knownFindings {
			t.Log(" - " + n.String())
		}
	}
	if len(newFindings) > 0 {
		var lines []string
		for _, n := range newFindings {
			lines = append(lines, n.String())
		}
		t.Fatalf("%d NEW down migration(s) narrow a CHECK constraint without remapping existing rows first (not in the known-debt allowlist — either fix the down.sql or, if this really is more pre-existing debt this scan just found, add it to knownPreExistingUnsafeNarrowings with a note):\n%s",
			len(newFindings), strings.Join(lines, "\n"))
	}
}
