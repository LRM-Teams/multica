UPDATE research_artifact_policy_state state
SET policy_version = 'research-v6-context-v1'
FROM research_session session
WHERE state.workspace_id=session.workspace_id AND state.session_id=session.id
  AND session.orchestrator_version='research-run-v6';
