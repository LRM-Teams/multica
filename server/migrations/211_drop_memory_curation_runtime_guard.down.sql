CREATE OR REPLACE FUNCTION public.memory_curation_runtime_guard_2h()
RETURNS trigger
LANGUAGE plpgsql
AS $function$
BEGIN
  IF OLD.status = 'running'
     AND NEW.status = 'failed'
     AND NEW.error IN ('memory curation exceeded server max runtime', 'memory curation exceeded max daemon claim attempts')
     AND OLD.started_at IS NOT NULL
     AND OLD.started_at > now() - interval '2 hours'
  THEN
    RETURN NULL;
  END IF;
  RETURN NEW;
END;
$function$;

DROP TRIGGER IF EXISTS trg_memory_curation_runtime_guard_2h ON memory_curation_run;
CREATE TRIGGER trg_memory_curation_runtime_guard_2h
  BEFORE UPDATE OF status, error ON memory_curation_run
  FOR EACH ROW EXECUTE FUNCTION memory_curation_runtime_guard_2h();
