BEGIN;

-- This migration repairs persisted job state. Reverting it would discard
-- successful retry progress and can mark valid transcripts as failed.

COMMIT;
