# Agent-creation Proposal cutover

This runbook deploys LRM-2343's Message-backed `agent:create` Proposal flow.
It replaces the live action-card state machine with a canonical channel Message
and a durable first-start intent. The migration archives old rows; it never
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
2. Deploy server and daemon together. The daemon must understand heartbeat
   `pending_agent_start_intents` and report `accepted`, then separately report
   `ready` or `failed` with monotonically increasing `lifecycle_seq`.
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
       asi.status AS start_intent_status,
       asi.lifecycle_seq,
       asi.failure_code
FROM agent_action aa
LEFT JOIN agent_start_intent asi ON asi.agent_id = aa.result_agent_id
WHERE aa.action_type = 'agent:create'
ORDER BY aa.prepared_at DESC
LIMIT 1;
```

Expected sequence: the Message part starts `prepared`; exactly one successful
commit changes it to `executed`, creates the Agent and `#general` membership,
and yields one intent. A Computer heartbeat accepts that same dispatch ID; a
separate local observation reports `ready`. A terminal `failed` row is retained
for a human to correct through normal Agent configuration/lifecycle controls;
the server must not keep redispatching it.

Finally, repeat the first preflight query. `agent_action_card` should be absent
and `agent_action_card_archive` present. Compare the archived-row count with
the recorded pre-migration count.
