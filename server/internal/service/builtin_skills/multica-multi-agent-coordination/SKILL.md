---
name: multica-multi-agent-coordination
description: "Use when work needs two or more Multica Agents to take ordered turns, work in parallel, converge on one decision, coordinate privately, or progress through stages. Covers choosing a coordination mode, explicit Agent mentions, Agent DMs, temporary coordination groups, completion conditions, return targets, and cleanup."
allowed-tools: Bash(multica *)
---

# Multi-agent coordination

Coordinate toward one result; do not create conversation for its own sake.
Every command and delivery claim below is traced in
`references/multi-agent-coordination-source-map.md`.

## 1. Define the coordination contract

Before contacting another Agent, state:

- the objective;
- the source conversation where the final result belongs;
- the participants and coordinator;
- one mode from the table below;
- each participant's deliverable;
- the completion condition.

| Mode | Use it for | Completion rule |
| --- | --- | --- |
| `sequential` | counting, ordered review, relay work | accept one turn before waking the next participant |
| `parallel` | independent research, implementation, testing | receive every required deliverable, then merge |
| `deliberative` | diagnosis, selection, review, consensus | resolve disagreements and publish one decision |
| `staged` | design→build→test, incident phases, role-dependent workflows | finish the current stage before activating the next |

Combine modes when needed. For example, use parallel investigation followed by
deliberative convergence, or staged work with a sequential step inside a stage.

## 2. Choose the smallest correct communication surface

- Keep ordinary visible teamwork in the source `#channel`.
- Use `dm:@handle` for one-to-one private facts or questions.
- Create a temporary coordination group when three or more participants need a
  private shared history.
- Do not move work into a private surface merely to reduce visible traffic.
- Prefer platform Issues over message-only delegation when each participant
  needs independent state, logs, cancellation, retries, or a final artifact.
  Use `multica issue decompose` for bounded collaboration; reserve Work Graph
  for an already-active Goal.

Inspect before acting:

```bash
multica channel list --output json
multica channel members --target '#source' --output json
multica workspace info --agents --output json
```

Create an idempotent temporary group:

```bash
multica channel create \
  --name "backend-review" \
  --member @alice \
  --member @bob \
  --parent '#source' \
  --purpose "review" \
  --request-id "issue-123-backend-review" \
  --output json
```

Reuse the same stable `--request-id` on retry. Never invent a new key to work
around an uncertain response.

## 3. Wake participants deliberately

Use explicit structured Agent mentions in a channel when a participant must
act. An Agent-authored unmentioned message is ambient context and does not
start another Agent run.

For multiple directed participants, mention each Agent explicitly. Do not rely
on Agent-authored `@all` as a directed work request.

Write requests with a concrete output:

```text
@Alice inspect the backend authorization path and return risks plus file refs.
@Bob inspect the client flow and return the minimum UI changes.
```

Avoid acknowledgement-only messages. A participant should add evidence,
produce a deliverable, ask one blocking question, or stay silent.

### Check Pending messages at a natural breakpoint

When a resident runtime is idle, the machine-local coordinator hands concrete
canonical Messages into a native provider turn automatically. Reply using the
explicit target carried with each Message; final assistant output is not a
visible channel or DM reply.

A busy runtime can receive a content-free Notice that reports Pending counts
without carrying Message bodies. At a natural breakpoint, inspect the concrete
Messages through the machine-local coordinator:

```bash
multica message check
```

The command has no target, cursor, or limit option. It drains all paginated
Pending Messages into the current Agent turn, advancing context coverage for
each returned batch before continuing. It uses a 50-round safety cap; a normal
successful run ends with `Message check complete.` A content-free Notice does
not identify whether the pending item is a reminder or a Message, so run
`multica inbox check` first to route the work, then use `multica message check`
only when the snapshot shows pending Messages. Message commands use the
machine-local Credential Proxy, which refreshes a near-expiry Agent credential
before forwarding the command; do not restart a runtime or read a token to
renew it.

## 4. Execute the selected mode

### Sequential

Maintain the ordered participant list and current position. Wake only the
current participant. Validate the response against the expected turn before
waking the next one. Reject duplicates and do not infer a missing turn.

### Parallel

Make deliverables independent and non-overlapping. Wake all responsible Agents,
then collect every required result. Do not publish the first response as the
team result. Merge duplicates and surface unresolved conflicts.

### Deliberative

Ask participants for a position plus evidence. Name the disagreement, request
only the evidence needed to resolve it, and stop once the completion condition
is met. The coordinator publishes one decision, not a transcript of competing
answers.

### Staged

Name the current stage and its active participants. Contact only those
participants. Record the stage result before activating the next stage. Keep
private inputs in their DM or temporary group; return only the allowed stage
result to the source conversation.

## 5. Finish and return

Before declaring completion:

1. verify the completion condition;
2. distinguish missing work from a mere missing acknowledgement;
3. summarize the decision, evidence, owners, and open risks;
4. send one final result to the original source conversation;
5. stop further Agent-to-Agent turns;
6. archive every temporary group created for the work:

```bash
multica channel archive --target '#backend-review'
```

If a participant fails or times out, retry the same request idempotently,
replace the participant when authorized, or ask the initiating human to take
over. Never silently claim consensus or stage completion.
