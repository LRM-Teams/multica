-- Wendy ambient (主动发现) hardening.
--
-- #1 debounce starvation: a busy channel that keeps chattering < debounce apart
--    never becomes due because review_not_before is pushed forward on every
--    message. first_dirty_message_at anchors the current unreviewed streak so a
--    max-staleness cap can force a review even under continuous chatter.
--
-- #2 lost reviews: dirty used to be cleared at radar-run *enqueue*. If the run
--    later failed the review was silently lost. reviewing_message_at records the
--    watermark a running review covers so completion can clear dirty precisely,
--    and failures can re-arm without advancing last_reviewed_message_at.
ALTER TABLE wendy_channel_ambient
  ADD COLUMN IF NOT EXISTS first_dirty_message_at TIMESTAMPTZ,
  ADD COLUMN IF NOT EXISTS reviewing_message_at TIMESTAMPTZ;

-- Anchor existing dirty rows so the staleness cap has a base immediately.
UPDATE wendy_channel_ambient
SET first_dirty_message_at = last_human_message_at
WHERE dirty = TRUE
  AND first_dirty_message_at IS NULL;
