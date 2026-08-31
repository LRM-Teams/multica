-- Integration writes an accepted typed Insight version in the same
-- transaction that first registers its Artifact Passport. Repair historical
-- V6 outputs that were left registered even though their current Insight
-- version was accepted.
UPDATE research_artifact_passport passport
SET lifecycle_status='accepted',
    accepted_at=COALESCE(passport.accepted_at,now())
FROM research_artifact_version version
JOIN research_insight_version insight
  ON insight.artifact_version_id=version.id
WHERE passport.entity_kind='insight'
  AND passport.lifecycle_status='registered'
  AND version.artifact_id=passport.id
  AND version.version=passport.current_version
  AND insight.status='accepted';
