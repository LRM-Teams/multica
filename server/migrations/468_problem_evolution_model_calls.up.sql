ALTER TABLE problem_evolution_run
    ADD COLUMN model_call_count INTEGER NOT NULL DEFAULT 0
    CHECK (model_call_count >= 0);
