ALTER TABLE research_report ADD COLUMN status TEXT NOT NULL DEFAULT 'draft',
 ADD COLUMN parent_report_id UUID, ADD COLUMN parent_revision INTEGER, ADD COLUMN title TEXT NOT NULL DEFAULT '',
 ADD COLUMN summary TEXT NOT NULL DEFAULT '', ADD COLUMN plain_text TEXT NOT NULL DEFAULT '',
 ADD COLUMN package_hash TEXT, ADD COLUMN document_content_hash TEXT, ADD COLUMN document_storage_key TEXT,
 ADD COLUMN document_storage_generation TEXT, ADD COLUMN document_byte_size BIGINT,
 ADD COLUMN input_snapshot_hash TEXT, ADD COLUMN csp_script_hashes JSONB NOT NULL DEFAULT '[]'::jsonb,
 ADD COLUMN csp_style_hashes JSONB NOT NULL DEFAULT '[]'::jsonb, ADD COLUMN input_event_sequence BIGINT,
 ADD COLUMN published_at TIMESTAMPTZ, ADD COLUMN reviewed_by_director_assignment_id UUID;
ALTER TABLE research_report ADD CONSTRAINT research_v6_report_status_check
 CHECK(status IN ('draft','published','needs_research','needs_revision','technical_failure'));
ALTER TABLE research_report ADD CONSTRAINT research_v6_report_parent_fk FOREIGN KEY(parent_report_id) REFERENCES research_report(id);
ALTER TABLE research_report ADD CONSTRAINT research_v6_report_director_fk
 FOREIGN KEY(workspace_id,session_id,reviewed_by_director_assignment_id) REFERENCES research_director_assignment(workspace_id,session_id,id);
CREATE UNIQUE INDEX research_v6_report_package_hash_idx ON research_report(session_id,package_hash) WHERE package_hash IS NOT NULL;
CREATE INDEX research_v6_report_latest_published_idx ON research_report(session_id,published_at DESC) WHERE status='published';

CREATE TABLE research_report_input (
 id UUID PRIMARY KEY DEFAULT gen_random_uuid(), workspace_id UUID NOT NULL, session_id UUID NOT NULL,
 report_id UUID NOT NULL, report_revision INTEGER NOT NULL, branch_id UUID NOT NULL, node_artifact_version_id UUID NOT NULL,
 input_role TEXT NOT NULL CHECK(input_role IN ('branch_xxl','branch_maximum','unresolved_gap')), ordinal INTEGER NOT NULL CHECK(ordinal>=0),
 content_hash TEXT NOT NULL CHECK(content_hash ~ '^sha256:[0-9a-f]{64}$'), created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 UNIQUE(workspace_id,session_id,id), UNIQUE(report_id,report_revision,node_artifact_version_id), UNIQUE(report_id,report_revision,ordinal),
 FOREIGN KEY(report_id) REFERENCES research_report(id) ON DELETE CASCADE,
 FOREIGN KEY(workspace_id,session_id,branch_id) REFERENCES research_branch(workspace_id,session_id,id)
);
CREATE TABLE research_report_resource (
 id UUID PRIMARY KEY DEFAULT gen_random_uuid(), workspace_id UUID NOT NULL, session_id UUID NOT NULL,
 report_id UUID NOT NULL, report_revision INTEGER NOT NULL, resource_id UUID NOT NULL, path TEXT NOT NULL,
 role TEXT NOT NULL CHECK(role IN ('document','script','style','image','font','data')), media_type TEXT NOT NULL,
 byte_size BIGINT NOT NULL CHECK(byte_size>=0), content_hash TEXT NOT NULL CHECK(content_hash ~ '^sha256:[0-9a-f]{64}$'),
 storage_key TEXT NOT NULL, storage_generation TEXT NOT NULL, upload_status TEXT NOT NULL CHECK(upload_status IN ('pending','verified','rejected')),
 created_at TIMESTAMPTZ NOT NULL DEFAULT now(), UNIQUE(workspace_id,session_id,id), UNIQUE(report_id,report_revision,resource_id),
 UNIQUE(report_id,report_revision,path), FOREIGN KEY(report_id) REFERENCES research_report(id) ON DELETE CASCADE,
 CHECK(path<>'' AND path NOT LIKE '/%' AND path NOT LIKE '%//%' AND path NOT LIKE '%../%')
);
CREATE TABLE research_report_review (
 id UUID PRIMARY KEY DEFAULT gen_random_uuid(), workspace_id UUID NOT NULL, session_id UUID NOT NULL,
 report_id UUID NOT NULL, report_revision INTEGER NOT NULL, director_assignment_id UUID NOT NULL,
 director_generation INTEGER NOT NULL CHECK(director_generation>=1), director_cycle_id UUID NOT NULL,
 input_state_version BIGINT NOT NULL CHECK(input_state_version>=0),
 decision TEXT NOT NULL CHECK(decision IN ('published','needs_research','needs_revision','technical_failure')),
 reason TEXT NOT NULL, render_artifact_version_id UUID, render_diagnostics JSONB NOT NULL DEFAULT '{}'::jsonb,
 follow_up_work_item_refs JSONB NOT NULL DEFAULT '[]'::jsonb, created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 UNIQUE(workspace_id,session_id,id), FOREIGN KEY(report_id) REFERENCES research_report(id) ON DELETE CASCADE,
 FOREIGN KEY(workspace_id,session_id,director_assignment_id) REFERENCES research_director_assignment(workspace_id,session_id,id),
 FOREIGN KEY(workspace_id,session_id,director_cycle_id) REFERENCES research_director_cycle(workspace_id,session_id,id)
);
CREATE UNIQUE INDEX research_v6_report_one_publish_review_idx ON research_report_review(report_id,report_revision) WHERE decision='published';

