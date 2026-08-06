-- Irreversible by design: after adoption, the structured binding is the
-- authority and may have been renamed. Automatically clearing it would revoke
-- a valid onboarding Agent and make rollback destructive.
SELECT 1;
