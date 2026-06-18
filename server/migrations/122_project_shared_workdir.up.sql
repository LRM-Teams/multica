-- Project shared working directory.
--
-- chat_session.project_id: the project a chat is currently "about". Nullable —
-- a chat with no project set behaves as a general conversation. Switchable from
-- the composer; cleared (not deleted) if the project is removed.
ALTER TABLE chat_session
  ADD COLUMN project_id UUID REFERENCES project(id) ON DELETE SET NULL;

CREATE INDEX idx_chat_session_project
  ON chat_session(project_id)
  WHERE project_id IS NOT NULL;

-- project_resource.managed: true for the Multica-provisioned shared working
-- directory (auto-created local_directory under the daemon's workspaces root),
-- as opposed to a directory/repo the user attached by hand. Lets the UI render
-- it as automatic and keeps it read-only.
ALTER TABLE project_resource
  ADD COLUMN managed BOOLEAN NOT NULL DEFAULT false;
