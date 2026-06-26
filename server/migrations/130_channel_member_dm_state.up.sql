-- Per-member DM state columns: hidden_at for soft-close ("remove from list"),
-- pinned_at for pinning a DM to the top of the DIRECT MESSAGES section.
-- Both are NULL by default (visible, unpinned). Only meaningful for DM channels
-- (kind = 'dm') but stored on all channel_member rows for simplicity.
-- ADD COLUMN ... DEFAULT NULL is a metadata-only operation on PostgreSQL 11+
-- (no table rewrite, no lock contention, instant).
ALTER TABLE channel_member
  ADD COLUMN hidden_at  TIMESTAMPTZ,
  ADD COLUMN pinned_at  TIMESTAMPTZ;
