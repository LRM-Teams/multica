# Env-Dispatch Scratch Channel Routing Design

## Problem

Scratch message dispatch creates a new group channel, adds the source agent as
the stable user-facing member, provisions an isolated sandbox runtime, and
clones a derived execution agent onto that runtime. The generic channel member
onboarding trigger currently treats the source agent addition as an ordinary
group-channel join.

While the sandbox is starting, channel onboarding can create a
`channel_agent_session` and wake the source agent on its shared runtime. After
the sandbox runtime registers, `CloneEnvDispatchAgentTx` migrates that session
from the source agent to the derived agent. Scratch provisioning then creates a
second derived-agent session and attempts to insert the same
`(channel_id, agent_id)` primary key. The rollout fails before its requested
message is created or its sandbox task is enqueued.

Production evidence from 2026-07-22 showed the race in this order:

1. The scratch channel and source-agent membership were committed.
2. Channel onboarding was published and claimed.
3. Onboarding created the source-agent channel session.
4. The sandbox daemon registered with its sandbox instance identity.
5. The clone migrated the source session to the derived agent.
6. Provisioning attempted a second derived session insert and received
   `channel_agent_session_pkey`.

## Goals

- A scratch env-dispatch channel must not generate ordinary agent onboarding.
- The source agent must not be awakened on its shared runtime for the synthetic
  env-dispatch membership.
- The requested group-channel message must route only to the derived agent on
  the isolated sandbox runtime.
- Provisioning must return the canonical channel chat session without creating
  duplicate or orphan sessions.
- A successful run must expose a DAG that reaches a terminal state and contains
  the sandbox task result.

## Non-goals

- Changing onboarding behavior for manually created channels.
- Removing the source agent from `channel_member`; it remains the stable mention
  alias presented to users.
- Changing branch dispatch session-copy semantics.
- Relaxing sandbox runtime readiness identity matching.

## Selected approach

Introduce `env_dispatch` as an explicit `channel_member.join_source`. The
channel onboarding trigger will return without creating its system message or
onboarding record when an agent membership has this source. Scratch
`CreateEnvDispatchChannel` will use this source for every synthetic agent
membership.

This is preferable to deleting onboarding artifacts before transaction commit:
the membership itself records why onboarding is suppressed, and future trigger
changes retain a clear policy boundary. It is also preferable to reusing the
onboarding-created session, because reuse would preserve an incorrect wake on
the source agent's shared runtime.

Provisioning will additionally use a focused get-or-create operation for the
derived agent's channel session. It first checks for an existing mapping. A
mapping is reusable only when its chat session belongs to the same workspace,
project, derived agent, and discovered sandbox runtime. If no mapping exists,
the operation creates the chat session and mapping atomically. A conflicting
winner is loaded and validated; any losing chat session is deleted before the
operation returns.

## Data model and migration

A new forward migration will:

1. Replace `channel_member_join_source_check` so it accepts the existing values
   plus `env_dispatch`.
2. Replace the combined INSERT/DELETE onboarding trigger with separate triggers.
   The INSERT trigger has a `WHEN (NEW.join_source <> 'env_dispatch')`
   predicate; the DELETE trigger retains the existing cleanup behavior. The
   underlying `maintain_channel_agent_onboarding()` function is unchanged.

The down migration will first rewrite any remaining `env_dispatch` membership
rows to `system`, restore the previous check constraint, and restore the
previous combined INSERT/DELETE trigger. This makes rollback possible without
violating the old constraint.

No existing membership rows are rewritten by the up migration.

## Request flow

The corrected scratch flow is:

1. Create the project, environment, and new group channel.
2. Add the requesting user normally.
3. Add source-agent members with `join_source='env_dispatch'` and create their
   env-dispatch bindings in the same transaction.
4. The onboarding trigger observes the explicit source and performs no
   onboarding side effects.
5. Provision the leader sandbox and wait for the daemon runtime identified by
   workspace, daemon ID, and sandbox instance ID.
6. Clone the source agent to a derived agent bound to that runtime.
7. Create or validate the derived agent's project-scoped channel chat session.
8. Mark the binding ready.
9. Insert the requested user channel message.
10. Enqueue the derived agent run with that message, session, runtime, sandbox,
    project, and environment identity.
11. Build the DAG from the resulting task and collaboration records.

## Error handling and cleanup

- Session validation fails closed if an existing mapping points at a different
  workspace, project, agent, or runtime.
- Chat session and channel mapping creation run in one transaction.
- If a concurrent insert wins, the losing chat session is deleted and the
  winner is validated before reuse.
- Existing provisioning compensation continues to remove the derived agent and
  sandbox when a later step fails.
- No `ON CONFLICT DO NOTHING` path may leave an unlinked chat session.

## Tests

Automated coverage will prove:

1. Env-dispatch channel creation records `join_source='env_dispatch'`.
2. The onboarding trigger produces no onboarding record, system join message,
   inbox event, or source-agent channel session for that membership.
3. Ordinary manual/system memberships still produce onboarding.
4. Scratch provisioning creates one derived-agent channel session when none
   exists.
5. A valid existing derived session is reused.
6. A mismatched session is rejected.
7. A concurrent mapping winner is returned without leaving an orphan session.
8. The scratch message service path creates the requested channel message,
   enqueues the derived agent with the sandbox runtime, and records the run in
   the DAG inputs.

After merge and automatic deployment, the live verification will run
`customized_areal/tree_search/agents/multica_client.py` against the dev
workspace and confirm the dispatch handle, channel message, sandbox-agent task
completion, and terminal DAG response. Test resources will be removed after
evidence is collected.

## Rollout

The migration and server change ship together through the Multica `dev` branch.
The change affects only memberships explicitly marked `env_dispatch`; all
existing channel creation behavior remains unchanged. Live verification begins
after the automatic deployment reports the new backend revision.
