# DM human-root thread wake

## Goal

Keep a one-to-one agent DM addressed to its agent when a human starts a thread
from an earlier human-authored message.

## Evidence and execution log

- [x] Traced human thread-reply creation and confirmed it implicitly followed
  the root author only when that author was an agent.
- [x] Traced ordinary DM thread dispatch and confirmed it wakes only active
  agent followers. A human-authored root therefore produced no agent wake
  unless the reply contained an explicit agent mention.
- [x] Reused `channelAgentMembers` and
  `followChannelThreadAgentUnlessExplicitlyUnfollowed` when the root belongs to
  a human in a DM. Human-to-human DMs remain unchanged because they have no
  agent member.
- [x] Preserved an agent's explicit thread unfollow; subsequent ordinary human
  replies do not silently re-follow or wake that agent.
- [x] Added a handler integration test covering the first ordinary reply wake,
  exact `thread_reply` priority, active follow state, explicit unfollow, and a
  later reply that remains silent.
- [x] Ran the new human-root test and the existing agent-root DM thread test
  against a new temporary database; both passed, and only that temporary
  database was removed afterward.
- [x] Ran `gofmt` and `git diff --check`.
- [x] Pushed `fix/dm-human-root-thread-wake` and opened ready-for-review PR #966
  into `dev`: <https://github.com/LRM-Teams/multica/pull/966>.

## Boundaries

- Group-thread follower rules are unchanged.
- Explicit mentions retain their existing directed-wake behavior.
- No server files, containers, or production data were modified.
