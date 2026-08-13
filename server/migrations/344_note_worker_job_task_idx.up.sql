-- Backfill for checkouts that already applied 338 before task_id indexed.
CREATE INDEX IF NOT EXISTS note_worker_job_task_idx
    ON note_worker_job(task_id)
    WHERE task_id IS NOT NULL;
