DROP TRIGGER IF EXISTS trg_member_delete_revoke_agent_credentials ON member;
DROP FUNCTION IF EXISTS revoke_agent_credentials_on_member_delete();

DROP TRIGGER IF EXISTS trg_agent_archive_revoke_credentials ON agent;
DROP FUNCTION IF EXISTS revoke_agent_credentials_on_archive();

DROP TRIGGER IF EXISTS trg_agent_credential_active_subject ON agent_credential;
DROP FUNCTION IF EXISTS enforce_agent_credential_active_subject();
