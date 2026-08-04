-- Preserve early dispatch failures as run-level evidence without fabricating
-- a derived-agent resource. Existing synthetic resources are converted only
-- when every linked event is a run-level lifecycle event; unexpected evidence
-- remains in place and causes the new resource-kind check to reject the
-- migration rather than deleting audit history.
ALTER TABLE env_dispatch_audit_event
    ALTER COLUMN audit_resource_id DROP NOT NULL;

UPDATE env_dispatch_audit_event AS event
SET audit_resource_id = NULL
FROM env_dispatch_audit_resource AS resource
WHERE event.audit_resource_id = resource.id
  AND resource.resource_kind = 'derived_agent'
  AND resource.resource_id = 'dispatch:' || resource.audit_id::text
  AND event.event_type IN ('creation_failed', 'rollback_started', 'dispatch_outcome');

DELETE FROM env_dispatch_audit_resource AS resource
WHERE resource.resource_kind = 'derived_agent'
  AND resource.resource_id = 'dispatch:' || resource.audit_id::text
  AND NOT EXISTS (
      SELECT 1
      FROM env_dispatch_audit_event AS event
      WHERE event.audit_resource_id = resource.id
  )
  AND NOT EXISTS (
      SELECT 1
      FROM env_dispatch_reclamation_obligation AS obligation
      WHERE obligation.audit_resource_id = resource.id
  );

ALTER TABLE env_dispatch_audit_resource
    DROP CONSTRAINT env_dispatch_audit_resource_resource_kind_check,
    ADD CONSTRAINT env_dispatch_audit_resource_resource_kind_check
        CHECK (resource_kind IN ('sandbox', 'runtime', 'binding', 'task', 'session'));

ALTER TABLE env_dispatch_audit_event
    ADD CONSTRAINT env_dispatch_audit_event_resource_required
        CHECK (
            audit_resource_id IS NOT NULL
            OR event_type IN ('creation_failed', 'rollback_started', 'dispatch_outcome')
        );
