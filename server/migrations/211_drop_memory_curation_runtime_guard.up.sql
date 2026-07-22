-- The 2h guard silently blocked failExpired updates for running rows
-- (RETURN NULL), so daemon claim/report loops left curation runs stuck in
-- `running` for up to two hours even after attempt/max-runtime expiry.
DROP TRIGGER IF EXISTS trg_memory_curation_runtime_guard_2h ON memory_curation_run;
DROP FUNCTION IF EXISTS memory_curation_runtime_guard_2h();
