# Voice Transcript Toggle

Date: 2026-07-22

## Goal

Agent voice replies render as voice-first messages: the transcript is hidden by default, a compact text-conversion action sits beside the voice bubble, and the revealed transcript is visually and semantically attached to that bubble.

## Boundaries

- Apply the behavior to Agent voice replies. Human voice input still exposes its transcript because Multica does not store the human recording and must not present a non-playable control as recorded audio.
- Keep transcript text as the canonical accessible/copyable message content; change only its default presentation.
- Preserve stickers and non-legacy attachments on mixed Agent voice messages.
- Use shared web/desktop views and semantic design tokens. Do not add app-specific copies or CSS-only icons.
- Change code locally, push a branch, and open a ready PR to `dev`. Do not modify the deployment server.

## Step log

### Step 1 — Reproduce and identify the rendering boundary

Status: complete

Evidence:

- A regression test using one Agent message with exactly `text + voice` reproduces the screenshot: the transcript is already in the DOM before any text-conversion action exists.
- The failure occurs at the shared `ChannelMessageBubble` seam: `MessageBody` renders the text part unconditionally and `VoiceMessageAudio` renders the voice bubble afterward.
- The fixture contains one text part, which rules out duplicate server persistence. The defect is a presentation-boundary decision.

### Step 2 — Implement the voice-first transcript interaction

Status: complete

Evidence:

- Agent voice replies now project their text/reference parts out of the ordinary body while leaving stickers and real attachments in their existing message positions.
- The voice control owns one local expanded/collapsed boolean. Its adjacent caption action uses `aria-expanded`/`aria-controls`; the revealed transcript is a labelled region under the same control group.
- The transcript region reuses `MessageBody` in transcript-only mode, so structured mentions, issue references, markdown, highlights, and copy semantics remain on the existing renderer instead of becoming a second text implementation.
- The visual relation uses a short primary-tinted rule, a shared bordered surface, and the visible “Voice transcript” label. All colors use semantic tokens and both icons come from the existing Lucide dependency.
- Human voice input behavior is unchanged because the original recording is not stored and cannot honestly offer playback.
- Focused regression passed: 86 tests passed with two existing skips across the channel bubble, voice control, attachment projection, and voice-presentation suites.

### Step 3 — Diagnose typed group-chat voice requests

Status: complete

Evidence:

- Read-only production inspection found that the user message `总结一下当前进度。用语音汇报给我` produced only text/reference parts. No voice part was persisted, so the renderer and TTS path never had a voice message to play.
- The stored execution prompt contained three conflicting text-only rules: directed replies required a `text answer`, sticker guidance required substantive answers to `send text only`, and ambient directed messages required a `plain-text reply`.
- The responding Beckham runtime reported `2026.07.20-8cc9c0b`, while the voice send contract was merged on 2026-07-22. This stale runtime does not contain `multica message send --voice`; prompt repair alone cannot add a missing CLI command to the already running binary.
- Directed, ambient, and unread channel prompts now preserve the requested supported delivery modality. Human typed turns also state that voice delivery exists and defer the exact command syntax to the runtime brief, avoiding a second CLI-command source in generic prompts.
- Structured human voice input retains the existing stronger per-turn `multica message send --voice` instruction.
- Regression tests cover structured voice input, typed voice intent, the absence of text-only conflicts, ambient/unread prompts, and the runtime-brief transport contract. The full server handler suite passed.

Operational requirement:

- After deployment, Beckham must reconnect with a Multica CLI/runtime build that contains `message send --voice`; the production runtime's development build will not auto-update itself.

### Step 4 — Verify frontend behavior and repository checks

Status: complete

Evidence so far:

- Focused views regression: 86 passed, two skipped.
- `@multica/views` TypeScript check passed.
- Full repository lint passed with only pre-existing warnings.
- React Doctor passed with zero findings after removing JSX-as-prop and using a semantic transcript region.
- A fresh disposable database applied every migration. The reused local database remains unable to replay migration 204 because it lacks its historical trigger; production already has migrations 169 and 204 plus the enabled trigger, so this is local fixture drift rather than a deploy defect.
- The fresh full check reached Go tests and exposed one unrelated macOS path assertion (`/var` versus `/private/var`) in `TestSecureSkillDraftBundleDirRejectsEscapes`; the isolated daemon test reproduces it. No product execution path was changed for that test-only canonical-path mismatch.
- After rebasing onto `origin/dev`, the newly merged env-dispatch persistence test failed independently because its direct adapter fixture omitted `SandboxInstanceID` while asserting that an instance marker was saved. Production service callers already supply the value. The fixture now supplies a unique instance ID and verifies that exact ID; the isolated regression and full handler suite pass.
- Rebase verification passed: 86 focused views tests (two skipped), views TypeScript, React Doctor, `cmd/multica`, `internal/messageparts`, and the complete handler suite on a fresh database. Both disposable databases were deleted after their runs.

### Step 5 — Publish PR and verify CI

Status: complete

Evidence:

- Branch `fix/voice-transcript-toggle` was rebased onto the latest `origin/dev` and pushed.
- Ready-for-review PR: https://github.com/LRM-Teams/multica/pull/918 targeting `dev`; GitHub reports it mergeable and not a draft.
- CI run `29910704430` passed on PR head `5d79296a2`: frontend, backend, Ubuntu installer, and macOS installer all completed successfully.
- On 2026-07-23 the branch was rebased again after `dev` advanced. The only conflict was an env-dispatch test fixture that upstream had also repaired; the resolved test retains the stricter exact sandbox-ID assertion. Post-rebase validation passed: 87 focused views tests (two skipped), views TypeScript, React Doctor, `cmd/multica`, `internal/messageparts`, and the full handler suite on a fresh disposable database.
