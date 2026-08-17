CREATE TABLE research_node_absorption (
 id UUID PRIMARY KEY DEFAULT gen_random_uuid(), workspace_id UUID NOT NULL, session_id UUID NOT NULL,
 input_artifact_version_id UUID NOT NULL, successor_insight_version_id UUID NOT NULL,
 integration_round_id UUID NOT NULL, discussion_id UUID,
 relation TEXT NOT NULL CHECK(relation IN ('promotion','assimilation','xxl_merge')),
 absorbed_at TIMESTAMPTZ NOT NULL DEFAULT now(), created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 UNIQUE(workspace_id,session_id,id), UNIQUE(session_id,input_artifact_version_id),
 FOREIGN KEY(workspace_id,session_id,successor_insight_version_id) REFERENCES research_insight_version(workspace_id,session_id,id)
);
CREATE INDEX research_v6_absorption_successor_idx ON research_node_absorption(session_id,successor_insight_version_id);

CREATE TABLE research_match_decision (
 id UUID PRIMARY KEY DEFAULT gen_random_uuid(), workspace_id UUID NOT NULL, session_id UUID NOT NULL,
 candidate_set_hash TEXT NOT NULL CHECK(candidate_set_hash ~ '^sha256:[0-9a-f]{64}$'),
 input_artifact_version_ids UUID[] NOT NULL CHECK(cardinality(input_artifact_version_ids)>=1),
 goal_version INTEGER NOT NULL CHECK(goal_version>=1), branch_scope_hash TEXT NOT NULL CHECK(branch_scope_hash ~ '^sha256:[0-9a-f]{64}$'),
 decision TEXT NOT NULL CHECK(decision IN ('matched','rejected','deferred')),
 reason_code TEXT NOT NULL CHECK(reason_code IN ('unrelated','no_semantic_gain','duplicate','blocked_by_scope','insufficient_evidence')),
 reason_detail TEXT NOT NULL, decided_by TEXT NOT NULL, director_cycle_id UUID,
 invalidated_at TIMESTAMPTZ, invalidated_reason TEXT NOT NULL DEFAULT '', created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 UNIQUE(workspace_id,session_id,id), FOREIGN KEY(workspace_id,session_id) REFERENCES research_session(workspace_id,id) ON DELETE CASCADE,
 FOREIGN KEY(workspace_id,session_id,director_cycle_id) REFERENCES research_director_cycle(workspace_id,session_id,id)
);
CREATE UNIQUE INDEX research_v6_match_current_idx ON research_match_decision(session_id,candidate_set_hash,goal_version,branch_scope_hash)
 WHERE invalidated_at IS NULL;

CREATE FUNCTION research_v6_absorption_guard_fn() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE output_session UUID; cursor_id UUID; depth INTEGER := 0;
BEGIN
 SELECT session_id INTO output_session FROM research_insight_version WHERE id=NEW.successor_insight_version_id;
 IF output_session IS DISTINCT FROM NEW.session_id THEN RAISE EXCEPTION 'cross-run absorption' USING ERRCODE='23514'; END IF;
 cursor_id := NEW.successor_insight_version_id;
 WHILE cursor_id IS NOT NULL AND depth < 1024 LOOP
   IF EXISTS(SELECT 1 FROM research_insight_version WHERE id=cursor_id AND artifact_version_id=NEW.input_artifact_version_id) THEN
     RAISE EXCEPTION 'absorption cycle' USING ERRCODE='23514';
   END IF;
   SELECT a.successor_insight_version_id INTO cursor_id FROM research_node_absorption a
    JOIN research_insight_version v ON v.artifact_version_id=a.input_artifact_version_id
    WHERE v.id=cursor_id LIMIT 1;
   depth := depth+1;
 END LOOP;
 IF depth>=1024 THEN RAISE EXCEPTION 'absorption graph depth exceeded' USING ERRCODE='54000'; END IF;
 RETURN NEW;
END;
$$;
CREATE CONSTRAINT TRIGGER research_v6_absorption_guard AFTER INSERT OR UPDATE ON research_node_absorption
 DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION research_v6_absorption_guard_fn();

