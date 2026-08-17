CREATE TABLE research_result_node (
 id UUID PRIMARY KEY DEFAULT gen_random_uuid(), workspace_id UUID NOT NULL, session_id UUID NOT NULL,
 result_artifact_id UUID NOT NULL, artifact_version_id UUID NOT NULL, work_item_attempt_id UUID NOT NULL,
 catalog_summary TEXT NOT NULL, brief_summary TEXT NOT NULL, objective TEXT NOT NULL, conclusion TEXT NOT NULL,
 content TEXT NOT NULL, scope JSONB NOT NULL DEFAULT '{}'::jsonb, uncertainties JSONB NOT NULL DEFAULT '[]'::jsonb,
 conflicts JSONB NOT NULL DEFAULT '[]'::jsonb, open_questions JSONB NOT NULL DEFAULT '[]'::jsonb,
 conclusion_state TEXT NOT NULL CHECK (conclusion_state IN ('proposed','accepted','challenged','refuted','invalid')),
 integration_state TEXT NOT NULL CHECK (integration_state IN ('unmatched','candidate','discussing','absorbed','excluded')),
 reason_code TEXT NOT NULL DEFAULT '', reason_detail TEXT NOT NULL DEFAULT '',
 content_hash TEXT NOT NULL CHECK (content_hash ~ '^sha256:[0-9a-f]{64}$'), accepted_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 created_at TIMESTAMPTZ NOT NULL DEFAULT now(), UNIQUE(workspace_id,session_id,id), UNIQUE(result_artifact_id),
 UNIQUE(artifact_version_id), UNIQUE(work_item_attempt_id),
 FOREIGN KEY (workspace_id,session_id) REFERENCES research_session(workspace_id,id) ON DELETE CASCADE,
 FOREIGN KEY (workspace_id,session_id,work_item_attempt_id) REFERENCES research_work_item_attempt(workspace_id,session_id,id)
);

CREATE TABLE research_insight_version (
 id UUID PRIMARY KEY DEFAULT gen_random_uuid(), workspace_id UUID NOT NULL, session_id UUID NOT NULL,
 insight_id UUID NOT NULL, revision INTEGER NOT NULL CHECK (revision>=1), artifact_version_id UUID NOT NULL,
 tier TEXT NOT NULL CHECK (tier IN ('M','L','XL','XXL')), catalog_summary TEXT NOT NULL, brief_summary TEXT NOT NULL,
 objective TEXT NOT NULL, conclusion TEXT NOT NULL, content TEXT NOT NULL, scope JSONB NOT NULL DEFAULT '{}'::jsonb,
 uncertainties JSONB NOT NULL DEFAULT '[]'::jsonb, conflicts JSONB NOT NULL DEFAULT '[]'::jsonb,
 open_questions JSONB NOT NULL DEFAULT '[]'::jsonb,
 status TEXT NOT NULL CHECK (status IN ('accepted','challenged','refuted','invalid','superseded','terminal')),
 integration_round_id UUID, discussion_id UUID, content_hash TEXT NOT NULL CHECK(content_hash ~ '^sha256:[0-9a-f]{64}$'),
 superseded_by_version_id UUID, created_at TIMESTAMPTZ NOT NULL DEFAULT now(), UNIQUE(workspace_id,session_id,id),
 UNIQUE(insight_id,revision), UNIQUE(artifact_version_id), UNIQUE(session_id,content_hash),
 FOREIGN KEY (workspace_id,session_id) REFERENCES research_session(workspace_id,id) ON DELETE CASCADE,
 FOREIGN KEY (insight_id) REFERENCES research_insight(id) ON DELETE CASCADE,
 FOREIGN KEY (workspace_id,session_id,superseded_by_version_id) REFERENCES research_insight_version(workspace_id,session_id,id)
);
ALTER TABLE research_insight ADD COLUMN current_version_id UUID;
ALTER TABLE research_insight ADD CONSTRAINT research_insight_current_v6_version_fk
 FOREIGN KEY(workspace_id,session_id,current_version_id) REFERENCES research_insight_version(workspace_id,session_id,id)
 DEFERRABLE INITIALLY DEFERRED;

CREATE TABLE research_node_branch (
 id UUID PRIMARY KEY DEFAULT gen_random_uuid(), workspace_id UUID NOT NULL, session_id UUID NOT NULL,
 node_artifact_version_id UUID NOT NULL, branch_id UUID NOT NULL, bound_by_decision_id UUID, bound_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 created_at TIMESTAMPTZ NOT NULL DEFAULT now(), UNIQUE(workspace_id,session_id,id), UNIQUE(session_id,node_artifact_version_id,branch_id),
 FOREIGN KEY(workspace_id,session_id) REFERENCES research_session(workspace_id,id) ON DELETE CASCADE,
 FOREIGN KEY(workspace_id,session_id,branch_id) REFERENCES research_branch(workspace_id,session_id,id) ON DELETE CASCADE
);
CREATE INDEX research_v6_node_branch_reverse_idx ON research_node_branch(session_id,node_artifact_version_id);

CREATE TABLE research_branch_frontier (
 id UUID PRIMARY KEY DEFAULT gen_random_uuid(), workspace_id UUID NOT NULL, session_id UUID NOT NULL,
 branch_id UUID NOT NULL, node_artifact_version_id UUID NOT NULL, tier TEXT NOT NULL CHECK(tier IN ('S','M','L','XL','XXL')),
 added_by_event_sequence BIGINT NOT NULL CHECK(added_by_event_sequence>=0), removed_by_event_sequence BIGINT,
 removal_reason TEXT NOT NULL DEFAULT '', created_at TIMESTAMPTZ NOT NULL DEFAULT now(), UNIQUE(workspace_id,session_id,id),
 FOREIGN KEY(workspace_id,session_id,branch_id) REFERENCES research_branch(workspace_id,session_id,id) ON DELETE CASCADE
);
CREATE UNIQUE INDEX research_v6_frontier_active_idx ON research_branch_frontier(session_id,branch_id,node_artifact_version_id)
 WHERE removed_by_event_sequence IS NULL;
CREATE INDEX research_v6_frontier_node_idx ON research_branch_frontier(session_id,node_artifact_version_id)
 WHERE removed_by_event_sequence IS NULL;

CREATE TABLE research_node_steward_assignment (
 id UUID PRIMARY KEY DEFAULT gen_random_uuid(), workspace_id UUID NOT NULL, session_id UUID NOT NULL,
 node_artifact_version_id UUID NOT NULL, agent_id UUID NOT NULL, membership_id UUID NOT NULL,
 generation INTEGER NOT NULL CHECK(generation>=1), status TEXT NOT NULL CHECK(status IN ('active','released','unavailable')),
 assigned_by_decision_id UUID, assigned_at TIMESTAMPTZ NOT NULL DEFAULT now(), released_at TIMESTAMPTZ,
 reason TEXT NOT NULL, created_at TIMESTAMPTZ NOT NULL DEFAULT now(), UNIQUE(workspace_id,session_id,id),
 FOREIGN KEY(workspace_id,session_id,membership_id) REFERENCES research_team_membership(workspace_id,session_id,id)
);
CREATE UNIQUE INDEX research_v6_steward_one_active_idx ON research_node_steward_assignment(session_id,node_artifact_version_id)
 WHERE status='active';

ALTER TABLE research_branch
 ADD COLUMN goal_version INTEGER, ADD COLUMN scope JSONB NOT NULL DEFAULT '{}'::jsonb,
 ADD COLUMN state_version BIGINT NOT NULL DEFAULT 0, ADD COLUMN reason_code TEXT NOT NULL DEFAULT '',
 ADD COLUMN reason_detail TEXT NOT NULL DEFAULT '', ADD COLUMN created_by_director_cycle_id UUID,
 ADD COLUMN created_by_attempt_id UUID, ADD COLUMN current_xxl_version_id UUID;
ALTER TABLE research_branch ADD CONSTRAINT research_v6_branch_cycle_fk
 FOREIGN KEY(workspace_id,session_id,created_by_director_cycle_id) REFERENCES research_director_cycle(workspace_id,session_id,id);
ALTER TABLE research_branch ADD CONSTRAINT research_v6_branch_attempt_fk
 FOREIGN KEY(workspace_id,session_id,created_by_attempt_id) REFERENCES research_work_item_attempt(workspace_id,session_id,id);
ALTER TABLE research_branch ADD CONSTRAINT research_v6_branch_xxl_fk
 FOREIGN KEY(workspace_id,session_id,current_xxl_version_id) REFERENCES research_insight_version(workspace_id,session_id,id)
 DEFERRABLE INITIALLY DEFERRED;

