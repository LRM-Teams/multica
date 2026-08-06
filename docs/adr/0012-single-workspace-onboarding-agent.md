---
status: accepted
---

# Bind one privileged Onboarding Agent to each Workspace

Each configured Workspace has one Onboarding Agent identified only by
`workspace.onboarding_agent_id`. The binding, rather than a display name such
as Wendy, is the authority for receiving hiring instructions and skills and
for preparing human-confirmable `agent:create` proposals. Ordinary Agents do
not receive that knowledge and are denied by the server even if they discover
the protocol. This deliberately differs from allowing every Agent to recruit:
it reduces prompt surface and prevents accidental or adversarial fleet growth
while preserving Owner/Admin direct human creation.

The Workspace Owner creates Wendy during the mandatory post-Workspace setup,
after connecting a Computer and choosing a Runtime and Model. Wendy creation,
the binding, core hiring skill, `#general` membership, and versioned welcome
messages commit atomically. Archiving preserves the binding and disables the
capability; only the sole Workspace Owner may archive or restore Wendy.

## Consequences

- Admins may edit Wendy's configuration but cannot create the initial Wendy,
  archive her, or restore her.
- Wendy may be renamed and may post Hiring Proposals in shared conversations;
  shared visibility does not grant ordinary Agents hiring knowledge or power.
- A Hiring Proposal and its created Agent commit in one transaction and the
  proposal is consumable exactly once.
- Workspaces without a bound candidate require Owner setup. A migration may
  bind one unambiguous legacy Wendy, but never guesses among multiple candidates.
