---
status: accepted
---

# Roll out Agent Command Policy without narrowing the existing CLI surface

The Agent Proxy credential authenticates one concrete Workspace, Agent, and
runtime scope. It is not a command allowlist and does not replace the Server's
role and Workspace authorization. Agent Command Policy is a separate,
launch-scoped projection used to describe and eventually enforce command
availability without making an incomplete catalog a new source of denial.

## Decision

The Machine Service owns a deep Agent Command Policy module. Callers ask it for
one decision for a stable command ID; they do not interpret a capability list
or combine policy flags themselves. Each decision has exactly one `state`:

- `legacy_passthrough`: the existing command path and Server authorization stay
  in force while that command has not completed policy migration;
- `allowed`: authoritative policy permits the command for this launch;
- `denied`: authoritative role or Workspace policy explicitly rejects it;
- `unavailable`: policy permits it, but this launch lacks a required local
  machine or runtime feature.

Missing and unknown entries are never interpreted as `denied`. During the
migration they resolve to `legacy_passthrough` for the versioned baseline of
commands that Agents could already invoke. Commands added after the baseline
must declare their audience and stable command ID when they join the CLI tree;
CI rejects an Agent-facing command with no policy classification.

The generated `multica` wrapper continues to forward the complete argument
vector to the real CLI. Policy is not accepted from
`MULTICA_AGENT_ACTIVE_CAPABILITIES`, another environment variable, a CLI flag,
or a request field. The Machine Service derives and snapshots decisions from
authenticated launch identity, Server-provided role and Workspace policy,
launch mode, and locally observed machine features. The Server remains the
final authorizer for every service-side action.

## Rollout gates

Enforcement remains disabled until all of these gates pass:

1. A generated inventory classifies every existing Agent-facing CLI leaf and
   explicitly excludes operator-only commands.
2. Every capability decision has an authoritative derivation; a handwritten
   subset such as only `message.read` and `message.send` is invalid.
3. Shadow mode shows no unexpected denial relative to the existing Agent CLI
   authorization contract across supported roles and launch modes.
4. Mixed-version tests prove that an older CLI, an older Machine Service, a
   missing policy snapshot, and an unknown command retain the versioned legacy
   behavior.
5. Contract tests prove wrapper argument transparency and baseline command
   parity, while negative tests prove that an explicit `denied` decision and an
   unavailable local feature fail closed with distinct reason codes.

The rollout sequence is `inventory -> shadow -> enforce classified commands`.
There is no switch from an incomplete set directly to allowlist enforcement.

## Consequences

Existing commands keep their current transport and authorization semantics
until their individual migration is complete. The compatibility path is
bounded by a versioned baseline rather than permanent fallback for future
commands. Capability observability may log command ID, state, policy version,
and bounded reason code, but never credentials, token paths, command payloads,
Message or Draft bodies, or unrestricted arguments.
