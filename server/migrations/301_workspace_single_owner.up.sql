-- A Workspace has one immutable Owner. Ownership transfer is deliberately not
-- part of this product contract, so ordinary membership writes may neither add
-- a second Owner nor remove the existing one.

CREATE UNIQUE INDEX member_workspace_single_owner_uidx
    ON member (workspace_id)
    WHERE role = 'owner';

CREATE OR REPLACE FUNCTION member_assert_exactly_one_workspace_owner()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
    affected_workspace_id UUID := COALESCE(NEW.workspace_id, OLD.workspace_id);
    owner_count INTEGER;
BEGIN
    -- Workspace deletion cascades memberships. Once the parent is gone there
    -- is no Workspace invariant left to enforce.
    IF NOT EXISTS (SELECT 1 FROM workspace WHERE id = affected_workspace_id) THEN
        RETURN NULL;
    END IF;

    SELECT count(*) INTO owner_count
    FROM member
    WHERE workspace_id = affected_workspace_id AND role = 'owner';

    IF owner_count <> 1 THEN
        RAISE EXCEPTION 'workspace % must have exactly one owner', affected_workspace_id
            USING ERRCODE = '23514', CONSTRAINT = 'member_workspace_exactly_one_owner';
    END IF;
    RETURN NULL;
END;
$$;

CREATE CONSTRAINT TRIGGER member_workspace_exactly_one_owner
AFTER INSERT OR UPDATE OF role, workspace_id OR DELETE ON member
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION member_assert_exactly_one_workspace_owner();
