-- Children must be removed before their parents. All audit evidence is opt-in
-- and scoped to the workspace through env_dispatch_audit_run.
DROP TABLE env_dispatch_audit_event;
DROP TABLE env_dispatch_reclamation_obligation;
DROP TABLE env_dispatch_audit_resource;
DROP TABLE env_dispatch_audit_run;
