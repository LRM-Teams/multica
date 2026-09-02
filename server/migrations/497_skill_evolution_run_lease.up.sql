-- 497: Orchestrator run leases and the admission-fixed pin (spec §12.6,
-- plan Slice 3.3).
--
-- One lease row per run: exactly one (owner, attempt) may drive the run
-- at a time, and re-acquisition after crash, response loss, or operator
-- seizure always increments the fencing token — a writer holding an
-- older attempt is refused by the store even if its own view of the
-- expiry has not lapsed. The trigger floor keeps attempts monotonic and
-- refuses to lease terminal runs; the store's CAS is the authority.
--
-- The pin guard freezes the admission-fixed columns of
-- skill_evolution_run: pinned inputs, key parts, and provenance never
-- mutate after admission (only status/updated_at/terminal_at advance via
-- the existing terminal guard), so checkpoint resume can compare pin
-- hashes against a pin that cannot have been edited under it.
--
-- Down is for pre-enable environments only (ADR 0021 D8).

CREATE TABLE skill_evolution_run_lease (
    workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    run_id UUID NOT NULL,
    owner_id TEXT NOT NULL CHECK (owner_id <> '' AND length(owner_id) <= 256),
    -- The fencing token: 1 on first acquisition, +1 on every
    -- re-acquisition, never lowered (trigger-enforced).
    attempt BIGINT NOT NULL CHECK (attempt >= 1),
    acquired_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at TIMESTAMPTZ NOT NULL CHECK (expires_at > acquired_at),
    heartbeat_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (workspace_id, run_id),
    CONSTRAINT skill_evolution_run_lease_run_fk
        FOREIGN KEY (workspace_id, run_id)
        REFERENCES skill_evolution_run (workspace_id, id)
        ON DELETE CASCADE
);

CREATE FUNCTION skill_evolution_run_lease_guard() RETURNS trigger AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'skill evolution run lease % cannot be deleted: releases expire, rows stay',
            OLD.run_id
            USING ERRCODE = 'raise_exception';
    END IF;
    IF TG_OP = 'UPDATE' THEN
        IF NEW.workspace_id <> OLD.workspace_id OR NEW.run_id <> OLD.run_id THEN
            RAISE EXCEPTION 'skill evolution run lease identity is immutable (run %)',
                OLD.run_id
                USING ERRCODE = 'raise_exception';
        END IF;
        IF NEW.attempt < OLD.attempt THEN
            RAISE EXCEPTION 'lease attempt is a fencing token: % would lower it to %',
                OLD.run_id, NEW.attempt
                USING ERRCODE = 'raise_exception';
        END IF;
        IF NEW.attempt = OLD.attempt AND NEW.owner_id <> OLD.owner_id THEN
            RAISE EXCEPTION 'lease owner changes only with a new attempt (run %)',
                OLD.run_id
                USING ERRCODE = 'raise_exception';
        END IF;
        IF NEW.attempt = OLD.attempt AND NEW.acquired_at <> OLD.acquired_at THEN
            RAISE EXCEPTION 'lease acquired_at changes only with a new attempt (run %)',
                OLD.run_id
                USING ERRCODE = 'raise_exception';
        END IF;
    END IF;
    IF EXISTS (SELECT 1 FROM skill_evolution_run r
               WHERE r.workspace_id = NEW.workspace_id AND r.id = NEW.run_id
                 AND r.status IN ('completed', 'no_action', 'rejected', 'cancelled',
                                  'failed', 'stale', 'fenced')) THEN
        RAISE EXCEPTION 'terminal run % cannot hold or renew a lease', NEW.run_id
            USING ERRCODE = 'raise_exception';
    END IF;    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER skill_evolution_run_lease_guard
    BEFORE INSERT OR UPDATE OR DELETE ON skill_evolution_run_lease
    FOR EACH ROW EXECUTE FUNCTION skill_evolution_run_lease_guard();

CREATE FUNCTION skill_evolution_run_pin_guard() RETURNS trigger AS $$
BEGIN
    IF NEW.workspace_id <> OLD.workspace_id OR NEW.id <> OLD.id
       OR NEW.target_agent_id <> OLD.target_agent_id
       OR NEW.task_type <> OLD.task_type
       OR NEW.environment_major_version <> OLD.environment_major_version
       OR NEW.pinned_inputs <> OLD.pinned_inputs
       OR NEW.created_by_actor <> OLD.created_by_actor
       OR NEW.created_at <> OLD.created_at THEN
        RAISE EXCEPTION 'skill evolution run % is admission-fixed: only status may advance', OLD.id
            USING ERRCODE = 'raise_exception';
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER skill_evolution_run_pin_guard
    BEFORE UPDATE ON skill_evolution_run
    FOR EACH ROW EXECUTE FUNCTION skill_evolution_run_pin_guard();
