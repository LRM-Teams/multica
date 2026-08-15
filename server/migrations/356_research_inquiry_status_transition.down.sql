DROP TRIGGER IF EXISTS research_question_status_guard ON research_question;

CREATE OR REPLACE FUNCTION research_inquiry_transition_allowed(
  entity_kind TEXT, old_status TEXT, new_status TEXT
) RETURNS BOOLEAN LANGUAGE sql IMMUTABLE AS $$
  SELECT old_status = new_status OR CASE entity_kind
    WHEN 'hypothesis' THEN CASE old_status
      WHEN 'proposed' THEN new_status IN ('investigating','obsolete')
      WHEN 'investigating' THEN new_status IN ('supported','weakened','refuted','conditional','obsolete')
      WHEN 'supported' THEN new_status IN ('investigating','weakened','refuted','conditional','obsolete')
      WHEN 'weakened' THEN new_status IN ('investigating','supported','refuted','conditional','obsolete')
      WHEN 'refuted' THEN new_status IN ('investigating','obsolete')
      WHEN 'conditional' THEN new_status IN ('investigating','supported','weakened','refuted','obsolete')
      ELSE false
    END
    WHEN 'branch' THEN CASE old_status
      WHEN 'proposed' THEN new_status IN ('active','terminated','obsolete')
      WHEN 'active' THEN new_status IN ('paused','completed','terminated','obsolete')
      WHEN 'paused' THEN new_status IN ('active','terminated','obsolete')
      WHEN 'completed' THEN new_status = 'obsolete'
      WHEN 'terminated' THEN new_status = 'obsolete'
      ELSE false
    END
    WHEN 'insight' THEN CASE old_status
      WHEN 'proposed' THEN new_status IN ('accepted','obsolete')
      WHEN 'accepted' THEN new_status IN ('stale','superseded','obsolete')
      WHEN 'stale' THEN new_status IN ('accepted','superseded','obsolete')
      WHEN 'superseded' THEN new_status = 'obsolete'
      ELSE false
    END
    ELSE false
  END;
$$;

DROP TRIGGER IF EXISTS research_inquiry_status_evidence_immutable ON research_inquiry_status_evidence;
DROP TRIGGER IF EXISTS research_inquiry_status_transition_immutable ON research_inquiry_status_transition;
DROP FUNCTION IF EXISTS research_inquiry_status_audit_append_only();
DROP TRIGGER IF EXISTS research_inquiry_status_evidence_target_guard ON research_inquiry_status_evidence;
DROP FUNCTION IF EXISTS research_inquiry_status_evidence_guard();
DROP TRIGGER IF EXISTS research_inquiry_status_transition_target_guard ON research_inquiry_status_transition;
DROP FUNCTION IF EXISTS research_inquiry_status_transition_target_guard();
DROP TABLE IF EXISTS research_inquiry_status_evidence;
DROP TABLE IF EXISTS research_inquiry_status_transition;
