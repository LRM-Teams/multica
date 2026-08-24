DO $$
BEGIN
  RAISE EXCEPTION 'migration 444 is irreversible: removed Activity facts and probe state cannot be reconstructed';
END $$;
