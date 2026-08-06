-- name: GetTaskMessageAtSeq :one
-- The task_message row whose seq == @seq for the source agent's task. Its
-- created_at is the cutoff timestamp used to roll issue fields back to their
-- value at the branch point (see IssueForkService.ForkIssueSubtree).
SELECT * FROM task_message
WHERE task_id = @task_id
  AND seq = @seq;

-- name: ListActivityLogForIssueAfter :many
-- Activity log entries for the source issue that happened strictly AFTER the
-- branch-point cutoff, oldest-first. Reconstruction rolls the current issue
-- back to its value at the cutoff: for each tracked field, the earliest
-- post-cutoff change records (in its "from") the value that was live at the
-- branch point.
SELECT * FROM activity_log
WHERE issue_id = @issue_id
  AND created_at > @cutoff
ORDER BY created_at ASC, id ASC;

-- name: CreateForkedIssue :one
-- Insert a new issue row that records its fork provenance. The caller supplies
-- every field explicitly: append-only/structural fields are copied from the
-- source issue, while fields that can be overwritten after creation are first
-- reconstructed to their value at the branch point from the activity log.
INSERT INTO issue (
    workspace_id, title, description, status, priority,
    assignee_type, assignee_id, creator_type, creator_id,
    parent_issue_id, acceptance_criteria, context_refs, position,
    start_date, due_date, metadata, number, project_id,
    forked_from_issue_id, forked_at_seq, forked_at_task_id
) VALUES (
    @workspace_id, @title, @description, @status, @priority,
    @assignee_type, @assignee_id, @creator_type, @creator_id,
    @parent_issue_id, @acceptance_criteria, @context_refs, @position,
    @start_date, @due_date, @metadata, @number, @project_id,
    @forked_from_issue_id, @forked_at_seq, @forked_at_task_id
) RETURNING *;

-- name: DeleteForkedIssue :exec
-- Delete a forked issue by id. The forked_from_issue_id IS NOT NULL guard makes
-- this a no-op on an original issue, so this endpoint can never delete a
-- non-fork even if handed a bad id.
DELETE FROM issue
WHERE id = @id
  AND forked_from_issue_id IS NOT NULL;
