CREATE UNIQUE INDEX sandbox_job_one_active_delete_per_instance
ON sandbox_job(instance_id)
WHERE type = 'delete' AND status IN ('queued', 'dispatched', 'running');
