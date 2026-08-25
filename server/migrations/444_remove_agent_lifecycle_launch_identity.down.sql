DO $$
BEGIN
  RAISE EXCEPTION 'migration 444 is irreversible: removed launch process identity cannot be reconstructed honestly';
END $$;
