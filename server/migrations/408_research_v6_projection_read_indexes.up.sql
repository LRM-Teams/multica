CREATE INDEX research_v6_projection_work_scan_idx
 ON research_work_item(session_id,id) INCLUDE(kind,status,updated_at);
CREATE INDEX research_v6_projection_result_scan_idx
 ON research_result_node(session_id,id) INCLUDE(artifact_version_id,work_item_attempt_id,accepted_at);
CREATE INDEX research_v6_projection_insight_scan_idx
 ON research_insight_version(session_id,id) INCLUDE(artifact_version_id,insight_id,revision,tier,status,created_at);
CREATE INDEX research_v6_projection_expiry_idx
 ON research_projection_snapshot(expires_at,id);
