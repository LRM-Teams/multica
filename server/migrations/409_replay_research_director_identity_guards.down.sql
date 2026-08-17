-- Migration 387 owns these guards. Rolling back this replay must not remove
-- schema that 387 legitimately installed on fresh databases.
SELECT 1;
