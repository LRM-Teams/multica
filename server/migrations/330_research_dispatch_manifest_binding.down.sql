-- Roll back Chapter D dispatch manifest binding.

ALTER TABLE research_dispatch_outbox DROP CONSTRAINT IF EXISTS research_dispatch_outbox_manifest_fkey;
ALTER TABLE research_dispatch_outbox DROP CONSTRAINT IF EXISTS research_dispatch_outbox_manifest_hash_check;
ALTER TABLE research_dispatch_outbox DROP COLUMN IF EXISTS manifest_id;
ALTER TABLE research_dispatch_outbox DROP COLUMN IF EXISTS manifest_hash;
