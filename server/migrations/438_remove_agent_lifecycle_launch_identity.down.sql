DO $$
BEGIN
  RAISE EXCEPTION 'migration 438 is irreversible: removed launch process identity cannot be reconstructed honestly';
END $$;
