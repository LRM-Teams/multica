-- 496: Skill evolution retention & eligibility (spec §12.2/§12.11/§12.12).
--
-- Diagnostic provider thinking gets a hard 30-day platform ceiling in the
-- retention policy (workspaces may shorten, never lengthen past it), the
-- sweep cursor learns a thinking stream, the trajectory eligibility ledger
-- persists the run-start pins (revocable, never re-grantable), and backfill
-- jobs record audited checkpoints (dry-run first). Down drops the new
-- tables/columns for pre-enable environments only (ADR 0021 D8).

-- Thinking retention: platform ceiling 30 days, per-workspace shortening
-- only. Existing policy versions bind to the default (30) — no existing
-- commitment is lengthened.
ALTER TABLE memory_retention_policy
    ADD COLUMN diagnostic_thinking_days int NOT NULL DEFAULT 30
        CHECK (diagnostic_thinking_days > 0 AND diagnostic_thinking_days <= 30);

ALTER TABLE memory_retention_sweep_cursor
    ADD COLUMN last_thinking_sweep_at timestamptz;

-- Trajectory eligibility ledger: the run-start pin made durable (spec
-- §12.2). Everything except the revocation columns is frozen by trigger;
-- deletion is refused. There is intentionally no path that grants or
-- widens eligibility after run start.
CREATE TABLE skill_trajectory_eligibility (
    workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    run_id UUID NOT NULL,
    run_kind TEXT NOT NULL CHECK (run_kind <> '' AND length(run_kind) <= 128),
    evolution_eligible BOOLEAN NOT NULL,
    allowed_purposes TEXT[] NOT NULL DEFAULT '{}',
    task_type TEXT NOT NULL DEFAULT '',
    lineage_id TEXT NOT NULL DEFAULT '',
    fixed_at TIMESTAMPTZ NOT NULL,
    fixed_by_actor TEXT NOT NULL CHECK (fixed_by_actor <> ''),
    revoked_by_actor TEXT NOT NULL DEFAULT '',
    revoked_at TIMESTAMPTZ,
    revoked_reason TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (workspace_id, run_id),
    CONSTRAINT skill_trajectory_eligibility_purposes_check CHECK (
        allowed_purposes <@ ARRAY['skill_evolution', 'evaluation_audit', 'curator_review']
        AND (NOT evolution_eligible OR cardinality(allowed_purposes) >= 1)
    )
);

CREATE INDEX idx_skill_trajectory_eligibility_workspace
    ON skill_trajectory_eligibility (workspace_id, fixed_at DESC);

CREATE FUNCTION skill_trajectory_eligibility_update_guard() RETURNS trigger AS $$
BEGIN
    IF NEW.workspace_id <> OLD.workspace_id OR NEW.run_id <> OLD.run_id
       OR NEW.run_kind <> OLD.run_kind OR NEW.allowed_purposes <> OLD.allowed_purposes
       OR NEW.task_type <> OLD.task_type OR NEW.lineage_id <> OLD.lineage_id
       OR NEW.fixed_at <> OLD.fixed_at OR NEW.fixed_by_actor <> OLD.fixed_by_actor THEN
        RAISE EXCEPTION 'skill trajectory eligibility run % is pinned at start: only revocation may change it', OLD.run_id
            USING ERRCODE = 'raise_exception';
    END IF;
    IF OLD.revoked_at IS NOT NULL THEN
        RAISE EXCEPTION 'skill trajectory eligibility run % is already revoked', OLD.run_id
            USING ERRCODE = 'raise_exception';
    END IF;
    IF NEW.evolution_eligible AND NOT OLD.evolution_eligible THEN
        RAISE EXCEPTION 'skill trajectory eligibility run % cannot be granted after run start', OLD.run_id
            USING ERRCODE = 'raise_exception';
    END IF;
    IF NEW.revoked_at IS NOT NULL AND NEW.evolution_eligible THEN
        RAISE EXCEPTION 'skill trajectory eligibility run % must flip eligible=false when revoked', OLD.run_id
            USING ERRCODE = 'raise_exception';
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER skill_trajectory_eligibility_update_guard
    BEFORE UPDATE ON skill_trajectory_eligibility
    FOR EACH ROW EXECUTE FUNCTION skill_trajectory_eligibility_update_guard();

CREATE TRIGGER skill_trajectory_eligibility_no_delete
    BEFORE DELETE ON skill_trajectory_eligibility
    FOR EACH ROW EXECUTE FUNCTION skill_ledger_append_only();

-- Backfill checkpoints (spec §12.12): audited job reports with actor,
-- policy, watermark, and selection counts. Dry-run reports and executed
-- backfills append immutable rows — the mode column tells them apart.
CREATE TABLE skill_backfill_checkpoint (
    workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    job_id TEXT NOT NULL CHECK (job_id <> '' AND length(job_id) <= 256),
    kind TEXT NOT NULL CHECK (kind IN ('trajectory_eligibility', 'legacy_skill_projection')),
    mode TEXT NOT NULL CHECK (mode IN ('dry_run', 'executed')),
    actor TEXT NOT NULL CHECK (actor <> ''),
    policy_version TEXT NOT NULL DEFAULT '',
    source_watermark TEXT NOT NULL DEFAULT '' CHECK (length(source_watermark) <= 256),
    selected_count INT NOT NULL DEFAULT 0 CHECK (selected_count >= 0),
    rejected_count INT NOT NULL DEFAULT 0 CHECK (rejected_count >= 0),
    reason TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (workspace_id, job_id)
);

CREATE INDEX idx_skill_backfill_checkpoint_workspace
    ON skill_backfill_checkpoint (workspace_id, created_at DESC);

CREATE TRIGGER skill_backfill_checkpoint_append_only
    BEFORE UPDATE OR DELETE ON skill_backfill_checkpoint
    FOR EACH ROW EXECUTE FUNCTION skill_ledger_append_only();
