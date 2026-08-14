CREATE TABLE research_discussion (
 id UUID PRIMARY KEY DEFAULT gen_random_uuid(), workspace_id UUID NOT NULL, session_id UUID NOT NULL,
 kind TEXT NOT NULL CHECK(kind IN ('match','promotion','assimilation','dispute')),
 scope_hash TEXT NOT NULL CHECK(scope_hash ~ '^sha256:[0-9a-f]{64}$'), input_set_hash TEXT NOT NULL CHECK(input_set_hash ~ '^sha256:[0-9a-f]{64}$'),
 goal_version INTEGER NOT NULL CHECK(goal_version>=1), branch_scope_hash TEXT NOT NULL CHECK(branch_scope_hash ~ '^sha256:[0-9a-f]{64}$'),
 through_event_sequence BIGINT NOT NULL CHECK(through_event_sequence>=0), revision INTEGER NOT NULL CHECK(revision>=1),
 status TEXT NOT NULL CHECK(status IN ('active','consensus_accept','consensus_reject','uncertain','escalated','stale_input','completed')),
 director_assignment_id UUID, stale_reason TEXT NOT NULL DEFAULT '', created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 updated_at TIMESTAMPTZ NOT NULL DEFAULT now(), UNIQUE(workspace_id,session_id,id),
 FOREIGN KEY(workspace_id,session_id) REFERENCES research_session(workspace_id,id) ON DELETE CASCADE,
 FOREIGN KEY(workspace_id,session_id,director_assignment_id) REFERENCES research_director_assignment(workspace_id,session_id,id)
);
CREATE UNIQUE INDEX research_v6_discussion_one_active_idx ON research_discussion(session_id,scope_hash,input_set_hash,goal_version,branch_scope_hash)
 WHERE status='active';

CREATE TABLE research_discussion_input (
 id UUID PRIMARY KEY DEFAULT gen_random_uuid(), workspace_id UUID NOT NULL, session_id UUID NOT NULL,
 discussion_id UUID NOT NULL, node_artifact_version_id UUID NOT NULL, ordinal INTEGER NOT NULL CHECK(ordinal>=0),
 tier TEXT NOT NULL CHECK(tier IN ('S','M','L','XL','XXL')), content_hash TEXT NOT NULL CHECK(content_hash ~ '^sha256:[0-9a-f]{64}$'),
 created_at TIMESTAMPTZ NOT NULL DEFAULT now(), UNIQUE(workspace_id,session_id,id), UNIQUE(discussion_id,node_artifact_version_id),
 UNIQUE(discussion_id,ordinal), FOREIGN KEY(workspace_id,session_id,discussion_id) REFERENCES research_discussion(workspace_id,session_id,id) ON DELETE CASCADE
);
CREATE TABLE research_discussion_participant (
 id UUID PRIMARY KEY DEFAULT gen_random_uuid(), workspace_id UUID NOT NULL, session_id UUID NOT NULL,
 discussion_id UUID NOT NULL, agent_id UUID NOT NULL, membership_id UUID NOT NULL, steward_assignment_id UUID,
 joined_ordinal INTEGER NOT NULL CHECK(joined_ordinal>=0), state TEXT NOT NULL CHECK(state IN ('active','absent','completed')),
 absence_reason TEXT NOT NULL DEFAULT '', created_at TIMESTAMPTZ NOT NULL DEFAULT now(), UNIQUE(workspace_id,session_id,id),
 UNIQUE(discussion_id,agent_id), UNIQUE(discussion_id,joined_ordinal),
 FOREIGN KEY(workspace_id,session_id,discussion_id) REFERENCES research_discussion(workspace_id,session_id,id) ON DELETE CASCADE,
 FOREIGN KEY(workspace_id,session_id,membership_id) REFERENCES research_team_membership(workspace_id,session_id,id),
 FOREIGN KEY(workspace_id,session_id,steward_assignment_id) REFERENCES research_node_steward_assignment(workspace_id,session_id,id)
);
CREATE TABLE research_discussion_turn (
 id UUID PRIMARY KEY DEFAULT gen_random_uuid(), workspace_id UUID NOT NULL, session_id UUID NOT NULL,
 discussion_id UUID NOT NULL, discussion_revision INTEGER NOT NULL, round INTEGER NOT NULL CHECK(round>=1), ordinal INTEGER NOT NULL CHECK(ordinal>=0),
 agent_id UUID NOT NULL, work_item_attempt_id UUID NOT NULL, manifest_id UUID NOT NULL,
 manifest_hash TEXT NOT NULL CHECK(manifest_hash ~ '^sha256:[0-9a-f]{64}$'), visible_message TEXT NOT NULL,
 contribution JSONB NOT NULL, evidence_refs JSONB NOT NULL DEFAULT '[]'::jsonb,
 payload_hash TEXT NOT NULL CHECK(payload_hash ~ '^sha256:[0-9a-f]{64}$'), client_request_id UUID NOT NULL,
 created_at TIMESTAMPTZ NOT NULL DEFAULT now(), UNIQUE(workspace_id,session_id,id), UNIQUE(client_request_id),
 UNIQUE(discussion_id,discussion_revision,round,ordinal),
 FOREIGN KEY(workspace_id,session_id,discussion_id) REFERENCES research_discussion(workspace_id,session_id,id) ON DELETE CASCADE,
 FOREIGN KEY(workspace_id,session_id,work_item_attempt_id) REFERENCES research_work_item_attempt(workspace_id,session_id,id)
);
CREATE TABLE research_discussion_vote (
 id UUID PRIMARY KEY DEFAULT gen_random_uuid(), workspace_id UUID NOT NULL, session_id UUID NOT NULL,
 discussion_id UUID NOT NULL, discussion_revision INTEGER NOT NULL, agent_id UUID NOT NULL,
 vote TEXT NOT NULL CHECK(vote IN ('accept','reject','uncertain')), reason TEXT NOT NULL, turn_id UUID NOT NULL,
 created_at TIMESTAMPTZ NOT NULL DEFAULT now(), UNIQUE(workspace_id,session_id,id), UNIQUE(discussion_id,discussion_revision,agent_id,turn_id),
 FOREIGN KEY(workspace_id,session_id,discussion_id) REFERENCES research_discussion(workspace_id,session_id,id) ON DELETE CASCADE,
 FOREIGN KEY(workspace_id,session_id,turn_id) REFERENCES research_discussion_turn(workspace_id,session_id,id)
);
CREATE INDEX research_v6_discussion_vote_current_idx ON research_discussion_vote(discussion_id,discussion_revision,agent_id,created_at DESC);

ALTER TABLE research_integration_round ADD COLUMN work_item_attempt_id UUID, ADD COLUMN goal_version_v6 INTEGER,
 ADD COLUMN branch_scope_hash TEXT, ADD COLUMN input_set_hash TEXT, ADD COLUMN mode TEXT,
 ADD COLUMN status_v6 TEXT, ADD COLUMN discussion_id_v6 UUID, ADD COLUMN output_insight_version_id UUID;

