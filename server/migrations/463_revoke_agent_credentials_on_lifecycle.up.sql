-- Durable Agent credentials now use sk_agent_. Existing daemon-issued mat_
-- rows are legacy launch credentials, not task/inbox delivery tokens (those
-- live in separate tables), and must not remain usable through the new path.
UPDATE agent_credential
SET revoked_at = COALESCE(revoked_at, now()),
    updated_at = now()
WHERE issuance_source = 'daemon'
  AND left(token_prefix, 4) = 'mat_'
  AND revoked_at IS NULL;

CREATE OR REPLACE FUNCTION revoke_daemon_agent_credentials_on_lifecycle()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
  IF (OLD.stopped_at IS NULL AND NEW.stopped_at IS NOT NULL)
     OR (OLD.runtime_id IS DISTINCT FROM NEW.runtime_id) THEN
    UPDATE agent_credential
    SET revoked_at = COALESCE(revoked_at, now()),
        updated_at = now()
    WHERE agent_id = NEW.id
      AND workspace_id = NEW.workspace_id
      AND issuance_source = 'daemon'
      AND revoked_at IS NULL;
  END IF;
  RETURN NEW;
END
$$;

CREATE TRIGGER trg_agent_credential_lifecycle_revoke
AFTER UPDATE OF stopped_at, runtime_id ON agent
FOR EACH ROW
EXECUTE FUNCTION revoke_daemon_agent_credentials_on_lifecycle();
