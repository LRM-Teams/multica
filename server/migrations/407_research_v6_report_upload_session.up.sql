ALTER TABLE research_report ADD COLUMN outline JSONB NOT NULL DEFAULT '[]'::jsonb,
 ADD COLUMN citations JSONB NOT NULL DEFAULT '[]'::jsonb;
ALTER TABLE research_report ADD CONSTRAINT research_v6_report_id_revision_unique UNIQUE(id,revision);
ALTER TABLE research_report_input ADD CONSTRAINT research_v6_report_input_revision_fk
 FOREIGN KEY(report_id,report_revision) REFERENCES research_report(id,revision) ON DELETE CASCADE;
ALTER TABLE research_report_input ADD CONSTRAINT research_v6_report_input_artifact_version_fk
 FOREIGN KEY(workspace_id,session_id,node_artifact_version_id)
 REFERENCES research_artifact_version(workspace_id,session_id,id);
ALTER TABLE research_report_resource ADD CONSTRAINT research_v6_report_resource_revision_fk
 FOREIGN KEY(report_id,report_revision) REFERENCES research_report(id,revision) ON DELETE CASCADE;
ALTER TABLE research_report_review ADD CONSTRAINT research_v6_report_review_revision_fk
 FOREIGN KEY(report_id,report_revision) REFERENCES research_report(id,revision) ON DELETE CASCADE;

CREATE TABLE research_report_upload_session (
 id UUID PRIMARY KEY DEFAULT gen_random_uuid(), workspace_id UUID NOT NULL, session_id UUID NOT NULL,
 work_item_id UUID NOT NULL, work_item_attempt_id UUID NOT NULL, agent_id UUID NOT NULL,
 report_id UUID NOT NULL, report_revision INTEGER NOT NULL, client_request_id UUID NOT NULL,
	completion_request_id UUID,
 path TEXT NOT NULL, role TEXT NOT NULL CHECK(role IN ('document','script','style','image','font','data')),
	media_type TEXT NOT NULL, byte_size BIGINT NOT NULL CHECK(byte_size>=0),
	content_hash TEXT NOT NULL CHECK(content_hash ~ '^sha256:[0-9a-f]{64}$'),
 storage_key TEXT NOT NULL, storage_generation TEXT NOT NULL DEFAULT '',
 status TEXT NOT NULL DEFAULT 'pending' CHECK(status IN ('pending','verified','rejected','expired')),
 capability_hash TEXT NOT NULL, expires_at TIMESTAMPTZ NOT NULL, failure_reason TEXT NOT NULL DEFAULT '',
 created_at TIMESTAMPTZ NOT NULL DEFAULT now(), completed_at TIMESTAMPTZ,
	UNIQUE(workspace_id,session_id,id), UNIQUE(workspace_id,session_id,client_request_id),
	UNIQUE(workspace_id,session_id,completion_request_id), UNIQUE(storage_key),
 FOREIGN KEY(workspace_id,session_id,work_item_attempt_id) REFERENCES research_work_item_attempt(workspace_id,session_id,id),
	FOREIGN KEY(report_id,report_revision) REFERENCES research_report(id,revision) ON DELETE CASCADE,
	CHECK(path<>'' AND path NOT LIKE '/%' AND path NOT LIKE '%//%' AND path NOT LIKE '%../%')
);
CREATE INDEX research_v6_report_upload_attempt_idx ON research_report_upload_session(work_item_attempt_id,status,expires_at);
ALTER TABLE research_report_resource ADD CONSTRAINT research_v6_report_resource_upload_fk
 FOREIGN KEY(workspace_id,session_id,resource_id)
 REFERENCES research_report_upload_session(workspace_id,session_id,id);
CREATE UNIQUE INDEX research_v6_report_review_cycle_idx ON research_report_review(report_id,report_revision,director_cycle_id);
