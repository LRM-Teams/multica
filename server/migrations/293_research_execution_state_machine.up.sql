-- Enforce Research execution status transitions at the canonical database
-- boundary. Application methods may add stricter preconditions, but no writer
-- can bypass these lifecycle invariants with a direct UPDATE.

CREATE FUNCTION research_task_status_transition_allowed(old_status TEXT, new_status TEXT)
RETURNS BOOLEAN
LANGUAGE sql
IMMUTABLE
STRICT
AS $$
  SELECT old_status = new_status OR (old_status, new_status) IN (
    ('pending', 'ready'),
    ('pending', 'blocked'),
    ('pending', 'obsolete'),
    ('pending', 'cancelled'),
    ('ready', 'pending'),
    ('ready', 'dispatching'),
    ('ready', 'obsolete'),
    ('ready', 'cancelled'),
    ('dispatching', 'running'),
    ('dispatching', 'succeeded'),
    ('dispatching', 'ready'),
    ('dispatching', 'failed'),
    ('dispatching', 'obsolete'),
    ('dispatching', 'cancelled'),
    ('running', 'succeeded'),
    ('running', 'ready'),
    ('running', 'failed'),
    ('running', 'obsolete'),
    ('running', 'cancelled'),
    ('failed', 'ready'),
    ('blocked', 'ready')
  )
$$;

CREATE FUNCTION research_attempt_status_transition_allowed(old_status TEXT, new_status TEXT)
RETURNS BOOLEAN
LANGUAGE sql
IMMUTABLE
STRICT
AS $$
  SELECT old_status = new_status OR (old_status, new_status) IN (
    ('dispatching', 'running'),
    ('dispatching', 'succeeded'),
    ('dispatching', 'failed'),
    ('dispatching', 'cancelled'),
    ('dispatching', 'lost'),
    ('running', 'succeeded'),
    ('running', 'failed'),
    ('running', 'cancelled'),
    ('running', 'lost')
  )
$$;

CREATE FUNCTION research_dispatch_status_transition_allowed(old_status TEXT, new_status TEXT)
RETURNS BOOLEAN
LANGUAGE sql
IMMUTABLE
STRICT
AS $$
  SELECT old_status = new_status OR (old_status, new_status) IN (
    ('pending', 'delivering'),
    ('pending', 'delivered'),
    ('pending', 'failed'),
    ('pending', 'cancelled'),
    ('delivering', 'pending'),
    ('delivering', 'delivered'),
    ('delivering', 'failed'),
    ('delivering', 'cancelled')
  )
$$;

CREATE FUNCTION enforce_research_task_status_transition()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
  IF NOT research_task_status_transition_allowed(OLD.status, NEW.status) THEN
    RAISE EXCEPTION 'illegal research_task status transition: % -> %', OLD.status, NEW.status
      USING ERRCODE = '23514', CONSTRAINT = 'research_task_status_transition_check';
  END IF;
  RETURN NEW;
END
$$;

CREATE FUNCTION enforce_research_attempt_status_transition()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
  IF NOT research_attempt_status_transition_allowed(OLD.status, NEW.status) THEN
    RAISE EXCEPTION 'illegal research_task_attempt status transition: % -> %', OLD.status, NEW.status
      USING ERRCODE = '23514', CONSTRAINT = 'research_task_attempt_status_transition_check';
  END IF;
  RETURN NEW;
END
$$;

CREATE FUNCTION enforce_research_dispatch_status_transition()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
  IF NOT research_dispatch_status_transition_allowed(OLD.status, NEW.status) THEN
    RAISE EXCEPTION 'illegal research_dispatch_outbox status transition: % -> %', OLD.status, NEW.status
      USING ERRCODE = '23514', CONSTRAINT = 'research_dispatch_outbox_status_transition_check';
  END IF;
  RETURN NEW;
END
$$;

CREATE TRIGGER research_task_status_transition_guard
BEFORE UPDATE OF status ON research_task
FOR EACH ROW
WHEN (OLD.status IS DISTINCT FROM NEW.status)
EXECUTE FUNCTION enforce_research_task_status_transition();

CREATE TRIGGER research_task_attempt_status_transition_guard
BEFORE UPDATE OF status ON research_task_attempt
FOR EACH ROW
WHEN (OLD.status IS DISTINCT FROM NEW.status)
EXECUTE FUNCTION enforce_research_attempt_status_transition();

CREATE TRIGGER research_dispatch_outbox_status_transition_guard
BEFORE UPDATE OF status ON research_dispatch_outbox
FOR EACH ROW
WHEN (OLD.status IS DISTINCT FROM NEW.status)
EXECUTE FUNCTION enforce_research_dispatch_status_transition();
