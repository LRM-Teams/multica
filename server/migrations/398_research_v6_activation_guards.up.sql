CREATE FUNCTION research_v6_append_only_guard_fn() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN RAISE EXCEPTION '% is append-only', TG_TABLE_NAME USING ERRCODE='55000'; END;
$$;
CREATE TRIGGER research_v6_result_node_append_only BEFORE UPDATE OR DELETE ON research_result_node
 FOR EACH ROW EXECUTE FUNCTION research_v6_append_only_guard_fn();
CREATE TRIGGER research_v6_absorption_append_only BEFORE UPDATE OR DELETE ON research_node_absorption
 FOR EACH ROW EXECUTE FUNCTION research_v6_append_only_guard_fn();
CREATE TRIGGER research_v6_discussion_turn_append_only BEFORE UPDATE OR DELETE ON research_discussion_turn
 FOR EACH ROW EXECUTE FUNCTION research_v6_append_only_guard_fn();
CREATE TRIGGER research_v6_report_review_append_only BEFORE UPDATE OR DELETE ON research_report_review
 FOR EACH ROW EXECUTE FUNCTION research_v6_append_only_guard_fn();
CREATE TRIGGER research_v6_steering_append_only BEFORE UPDATE OR DELETE ON research_steering_assessment
 FOR EACH ROW EXECUTE FUNCTION research_v6_append_only_guard_fn();

CREATE FUNCTION research_v6_branch_xxl_guard_fn() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE actual_tier TEXT; actual_status TEXT; bound BOOLEAN;
BEGIN
 IF NEW.current_xxl_version_id IS NULL THEN RETURN NEW; END IF;
 SELECT tier,status INTO actual_tier,actual_status FROM research_insight_version
  WHERE workspace_id=NEW.workspace_id AND session_id=NEW.session_id AND id=NEW.current_xxl_version_id;
 SELECT EXISTS(SELECT 1 FROM research_node_branch b JOIN research_insight_version v ON v.artifact_version_id=b.node_artifact_version_id
  WHERE b.workspace_id=NEW.workspace_id AND b.session_id=NEW.session_id AND b.branch_id=NEW.id AND v.id=NEW.current_xxl_version_id) INTO bound;
 IF actual_tier IS DISTINCT FROM 'XXL' OR actual_status IS DISTINCT FROM 'accepted' OR NOT bound THEN
  RAISE EXCEPTION 'current Branch XXL must be an accepted bound XXL' USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END;
$$;
CREATE CONSTRAINT TRIGGER research_v6_branch_xxl_guard AFTER INSERT OR UPDATE OF current_xxl_version_id ON research_branch
 DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION research_v6_branch_xxl_guard_fn();

CREATE FUNCTION research_v6_report_publish_guard_fn() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF NEW.status='published' THEN
  IF NEW.package_hash IS NULL OR NEW.document_content_hash IS NULL OR NEW.document_storage_key IS NULL
   OR NEW.input_snapshot_hash IS NULL OR NEW.published_at IS NULL
   OR NOT EXISTS(SELECT 1 FROM research_report_resource r WHERE r.report_id=NEW.id AND r.report_revision=NEW.revision AND r.role='document' AND r.upload_status='verified')
   OR NOT EXISTS(SELECT 1 FROM research_report_input i WHERE i.report_id=NEW.id AND i.report_revision=NEW.revision) THEN
    RAISE EXCEPTION 'published V6 report package is incomplete' USING ERRCODE='23514';
  END IF;
 END IF;
 RETURN NEW;
END;
$$;
CREATE CONSTRAINT TRIGGER research_v6_report_publish_guard AFTER INSERT OR UPDATE OF status ON research_report
 DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION research_v6_report_publish_guard_fn();

CREATE TABLE research_v6_activation_evidence (
 id UUID PRIMARY KEY DEFAULT gen_random_uuid(), requirement TEXT NOT NULL, evidence_id TEXT NOT NULL,
 revision TEXT NOT NULL, passed BOOLEAN NOT NULL, recorded_by UUID NOT NULL REFERENCES "user"(id),
 recorded_at TIMESTAMPTZ NOT NULL DEFAULT now(), UNIQUE(requirement,evidence_id,revision),
 CHECK(length(btrim(requirement))>0 AND length(btrim(evidence_id))>0 AND length(btrim(revision))>0)
);

