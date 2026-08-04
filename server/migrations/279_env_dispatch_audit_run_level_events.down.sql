-- Recreate the former synthetic correlation resource only to restore the
-- previous non-null event shape during a rollback of this migration.
ALTER TABLE env_dispatch_audit_resource
    DROP CONSTRAINT env_dispatch_audit_resource_resource_kind_check,
    ADD CONSTRAINT env_dispatch_audit_resource_resource_kind_check
        CHECK (resource_kind IN (
            'sandbox', 'runtime', 'binding', 'derived_agent', 'task', 'session'
        ));

INSERT INTO env_dispatch_audit_resource (audit_id, resource_kind, resource_id)
SELECT DISTINCT event.audit_id, 'derived_agent', 'dispatch:' || event.audit_id::text
FROM env_dispatch_audit_event AS event
WHERE event.audit_resource_id IS NULL
ON CONFLICT (audit_id, resource_kind, resource_id) DO NOTHING;

UPDATE env_dispatch_audit_event AS event
SET audit_resource_id = resource.id
FROM env_dispatch_audit_resource AS resource
WHERE event.audit_resource_id IS NULL
  AND resource.audit_id = event.audit_id
  AND resource.resource_kind = 'derived_agent'
  AND resource.resource_id = 'dispatch:' || event.audit_id::text;

ALTER TABLE env_dispatch_audit_event
    DROP CONSTRAINT env_dispatch_audit_event_resource_required,
    ALTER COLUMN audit_resource_id SET NOT NULL;
