-- name: GetSquadInWorkspace :one
-- Used only to hydrate the display name of a historical squad mention
-- (legacy markup, e.g. an old comment's mention:// reference, or a
-- historical issue.assignee_type='squad' row) — the squad product itself
-- is retired and no new squad-routed work is ever created.
SELECT * FROM squad WHERE id = $1 AND workspace_id = $2;
