# Composer voice test contract repair

## Goal

Restore the frontend CI contract without weakening the Composer rule that a
voice control needs a channel ID, playback scope, and send callback.

## Evidence and execution log

- [x] Read PR #948's failed frontend job instead of attributing it to the deploy
  diff.
- [x] Reproduced the exact failure with
  `pnpm --filter @multica/views exec vitest run channels/components/composer.test.tsx --reporter=verbose`.
- [x] Confirmed `Composer` renders `VoiceInputButton` only when
  `voiceChannelId`, `voicePlaybackScope`, and `onVoiceSend` are all present.
- [x] Confirmed the LRM-353 style test supplied the latter two props but omitted
  `voiceChannelId`, while the earlier functional microphone test supplied all
  three.
- [x] Added the missing channel identity to the test fixture; production code
  is unchanged.
- [x] Ran the focused Composer suite (11/11), `@multica/views` typecheck, and
  `git diff --check`; all passed.
- [x] Pushed `fix/composer-secure-context-test` and opened ready-for-review PR
  #952 into `dev`: <https://github.com/LRM-Teams/multica/pull/952>.

## Boundaries

- This change does not make the voice button render without a channel.
- This change does not alter browser capability checks, recording, ASR, TTS,
  or deployment behavior.
