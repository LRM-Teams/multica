-- The deterministic pinyin backfill runs in cmd/migrate's pre-migration hook.
-- No alias table or old-handle mapping is retained: historical raw text stays
-- historical text, while structured references retain immutable agent UUIDs.
SELECT 1;
