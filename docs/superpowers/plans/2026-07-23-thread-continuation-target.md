# Thread continuation transport target

## Goal

Give agents woken by an ordinary followed-thread reply the exact destination
required by the chat transport CLI.

## Evidence and execution log

- [x] Traced ordinary thread continuation dispatch to
  `buildChannelThreadContinuationPrompt` and confirmed it included the current
  message ID but omitted `Message target for chat transport`.
- [x] Confirmed the directed-mention path already uses
  `agentMessageTargetForPrompt`, which generates `#channel:<rootId>` for group
  threads and `dm:@handle:<rootId>` for DM threads.
- [x] Reused that existing target resolver in the continuation prompt instead
  of creating another target format.
- [x] Corrected the same prompt's location label so DM thread runs are not told
  that they are inside a group chat.
- [x] Added assertions for the exact group-thread target and a focused DM-thread
  prompt test covering the DM label, user handle, and root ID.
- [x] Ran both focused handler tests against a new temporary database and
  removed only that database afterward; both passed.
- [x] Ran `gofmt` and `git diff --check`.
- [x] Pushed `fix/thread-continuation-target` and opened ready-for-review PR
  #965 into `dev`: <https://github.com/LRM-Teams/multica/pull/965>.

## Boundaries

- This does not change which agents follow a thread or which messages wake
  them; it only makes an already-dispatched continuation executable.
- No server files, containers, or production data were modified.
