-- Operations created before the discrete Agent Restart contract do not carry
-- enough evidence to resume safely: scheduled rows may still belong to the
-- retired heartbeat dispatcher, and running rows may lack an exact stop
-- launch fence or reset completion proof. Fail closed and release the active
-- operation fence so the user can retry through the new API.
UPDATE agent_lifecycle_operation
SET status = 'failed',
    step = 'migration',
    reason_code = 'agent_restart_contract_upgraded_retry',
    started_at = COALESCE(started_at, now()),
    finished_at = now(),
    updated_at = now()
WHERE status IN ('scheduled', 'running');
