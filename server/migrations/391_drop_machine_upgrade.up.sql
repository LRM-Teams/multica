BEGIN;

-- Cloud Computer upgrades are dispatched on the current Binding socket.
-- Progress and completion no longer live in a durable receipt table.
DROP TABLE IF EXISTS machine_upgrade;

COMMIT;
