DO $$
BEGIN
  RAISE EXCEPTION 'migration 445 is irreversible: removed launch process identity cannot be reconstructed honestly';
END $$;
