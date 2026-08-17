# Agent-creation Proposal cutover

This runbook deploys LRM-2343's Message-backed `agent:create` Proposal flow.
It replaces the live action-card state machine with a canonical channel Message
and a durable desired Runner launch. The migration archives old rows; it never
deletes them during rollout.

## Before applying migrations

Run these read-only checks against the target database and record their output
in the deployment change. They are the source of truth for the old state that
will be archived.

```sql
SELECT to_regclass('public.agent_action_card') AS legacy_table,
       to_regclass('public.agent_action_card_archive') AS archive_table;

SELECT status, count(*)
FROM agent_action_card
GROUP BY status
ORDER BY status;

SELECT count(*) AS legacy_rows
FROM agent_action_card;
```

If `agent_action_card` is absent and `agent_action_card_archive` exists, the
archive migration has already run. Do not rename it back solely to repeat this
deployment. If both exist, stop: that is not an expected migration state.

## Deploy

1. Apply database migrations through `291_archive_agent_action_card` before
   serving a binary that has removed the old action-card endpoints.
2. Deploy server and daemon together. The Workspace Runner reports its current
   `runningAgents`; the server reconciles `agent_runner_launch_projection`
   through stable `launchId`-fenced `agent:start`/`agent:stop` commands.
   Migration 336 intentionally leaves the old start-intent and correlation
   columns as write-only rolling-deploy shadows; new code does not consume
   them. Remove that storage only after the old binary version floor is gone.
3. Deploy the web client. It renders the `agent:create` reference from the
   channel Message directly and submits its `message_id` as `action_message_id`
   when an owner or admin confirms the final configuration.

## Post-deploy verification

Use an owner/admin to prepare and commit one proposal in a test channel, then
verify the durable facts rather than only the HTTP response:

```sql
SELECT aa.status,
       aa.channel_message_id,
       aa.result_agent_id,
       launch.runtime_id,
       launch.launch_id
FROM agent_action aa
LEFT JOIN agent_runner_launch_projection launch ON launch.agent_id = aa.result_agent_id
WHERE aa.action_type = 'agent:create'
ORDER BY aa.prepared_at DESC
LIMIT 1;
```

Expected sequence: the Message part starts `prepared`; exactly one successful
commit changes it to `executed`, creates the Agent and `#general` membership,
and yields one desired launch. A connected Workspace Runner accepts that same
`launch_id` and separately reports active residency. Reconnect retries the
same launch; a Runtime move first stops the observed old launch and then starts
the new desired placement.

Finally, repeat the first preflight query. `agent_action_card` should be absent
and `agent_action_card_archive` present. Compare the archived-row count with
the recorded pre-migration count.
