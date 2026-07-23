# Voice Reply Production Fix

Date: 2026-07-22

## Goal

Make Agent voice replies render and play as voice messages on the deployed dev product at `http://82.157.184.89:8090/`: a duration-labelled, duration-sized chat bubble must play server-generated Doubao speech from the canonical transcript. Agent-generated audio attachments must not become the playback source.

## Boundaries

- Change code locally, publish a PR to `dev`, and let the existing CI/CD workflow deploy after merge.
- Use the `:8090` Multica deployment and its Multica containers only for read-only evidence and post-deploy verification.
- Do not edit application code, data, configuration, or files on the server.
- Keep the provider credential on the server boundary. Do not print, persist, or send it to clients.
- Treat the old runtime shape at the agent transport/API boundary. Do not add an internal catch-all or play unverified attachment bytes.

## Step log

### Step 1 — Reproduce the production failure from stored facts

Status: complete

Evidence:

- The dev deployment is served on port `8090`; the inspected frontend/backend image tag is `sha-b05729a`, which includes the previously merged structured-voice UI.
- The reported Wendy reply has canonical text `你好～`, parts `text + attachment`, and one hydrated `audio/mpeg` attachment named `nihao.mp3`. It has no `{type:"voice"}` part, so the current renderer correctly chooses the generic file card and never mounts the voice control.
- The exact stored MP3 has a valid MP3 container (22.05 kHz, mono, about 1.49 seconds), but converting its payload to the provider ASR input contract produces an empty transcript. The uploaded bytes are not recognizable speech.
- The responding runtime predates the `multica message send --voice` contract. It generated and uploaded an audio file itself instead of asking Multica to synthesize the canonical transcript.

Decision:

- The canonical path remains transcript + `{type:"voice"}` and server-owned TTS.
- At the agent transport boundary, the one legacy shape observed in production — Agent text plus exactly one owned audio attachment and no voice part — is normalized to a voice reply.
- At the display boundary, the same exact historical shape is projected as a voice reply so already-stored messages are repaired without editing production data. Its attachment is consumed as a modality signal and hidden; its bytes are never played.
- Current runtimes receive an explicit prohibition against synthesizing/uploading an audio attachment for a voice reply.

### Step 2 — Pin the production shape with regression tests

Status: complete

Evidence:

- Added a pure presentation test using the production message shape: Agent `你好～`, one text part, one `audio/mpeg` attachment part, and the hydrated `nihao.mp3` record.
- Added a message-body regression requiring the consumed audio attachment to disappear while the accessible transcript remains visible.
- The focused test run failed for the intended reasons: the presentation resolver did not exist and the generic attachment zone still rendered `nihao.mp3`. Eight unrelated attachment-zone tests stayed green.

### Step 3 — Implement protocol normalization and voice presentation

Status: complete

Evidence:

- Agent transport inspects only a two-part `text + attachment` candidate and adds a `voice` marker only when the attachment row belongs to the same Agent/workspace and its persisted content type starts with `audio/`.
- The shared web/desktop presentation resolver recognizes both canonical voice parts and the exact pre-marker production shape. The consumed legacy attachment is removed from full and compact message bodies; ordinary files, user audio, and mixed deliveries retain their existing rendering.
- Voice bubbles fetch server TTS bytes for the canonical transcript, read `X-Voice-Duration-Ms`, display the duration before a click, and grow from 112 px to 240 px as real duration increases. Playback decodes the server WAV; legacy attachment bytes are never decoded or played.
- Prepared TTS responses are deduplicated and kept in a bounded eight-entry in-memory cache. Provider preparation failure moves the bubble to an explicit retry state.
- The runtime brief and CLI help now state that `--voice` is the sole voice-reply path and prohibit Agent-side audio synthesis/upload.
- Focused verification passed: Core 47/47; Views 89/89 with two pre-existing skips; daemon runtime and CLI Go packages; and three Agent transport attachment/voice cases against a fully migrated disposable database. The disposable database was deleted after the run.

Unexpected issue review:

- The channel-bubble test fixture had no initialized API singleton, so background duration preparation failed with `synthesizeVoice is not a function`. Production initializes the complete `ApiClient`; the fixture now mocks the playback boundary instead of adding a product fallback.
- The shared local handler database has an old inconsistent migration ledger (`204_system_general_channel` absent while a trigger it drops is already absent). A new database applies the complete history and passes the new tests, so no migration or product code was changed for the dirty local database.

### Step 4 — Verify focused and repository-wide checks

Status: complete

Evidence:

- `pnpm react:doctor` reports zero issues in both changed React packages (`@multica/core` and `@multica/views`). It first identified a component-file helper export and an avoidable async wait; both were corrected before this final run.
- `pnpm lint` completed with zero errors. The seven warnings are in unchanged web/desktop/view files.
- `git diff --check` passed, and `gofmt -d` reports no Go formatting delta.
- The second repository-wide `make check` run passed the full TypeScript typecheck/test suite, all migrations through 207, and all Go packages including `internal/handler`, `internal/daemon`, the voice provider integration, and CLI.
- The Playwright phase did not pass: 4/19 passed. The failures are outside the changed files and reproduce two existing test-infrastructure mismatches: parallel fixtures delete/recreate the same `e2e@multica.ai` verification code and hit `429`/missing-code races; authenticated tests then land on the current first-use welcome screen while their selectors still expect the pre-onboarding issue/sidebar UI. The login-page and onboarding assertions also expect retired DOM/text (`h1 Multica`, `Step 1 of 6`) while the captured page shows the current `Sign in to Multica` form and `Step 1 of 5`. No voice production fallback or unrelated E2E rewrite was added.

Unexpected issue review:

- The first full run exposed a macOS-only assertion comparing unresolved `/var/...` with the security-resolved `/private/var/...` path in `TestSecureSkillDraftBundleDirRejectsEscapes`. The production function returned the correct canonical path, and its focused test passed when the expected test path was canonicalized. Because this is unrelated test portability and not a user-reachable bug, that exploratory edit was removed from this PR.
- Starting Next.js rewrote generated `apps/web/next-env.d.ts` from production route types to dev route types. That generated change was removed from this PR.

### Step 5 — Publish PR and verify CI

Status: complete

Evidence:

- Rebased cleanly onto current `origin/dev` at `8b31ba9c2`; the three overlapping test/runtime files merged without conflict.
- Post-rebase verification passed: Core 776/776; Views 2321 passed with five existing skips; daemon runtime/CLI packages; and the three Agent transport attachment/voice cases on a disposable database migrated through 208. The disposable database was deleted.
- Published branch `fix/voice-reply-rendering-audio` and opened ready-for-review PR [#906](https://github.com/LRM-Teams/multica/pull/906) targeting `dev`.
- CI status is tracked on the PR; deployment remains intentionally gated on the user's merge.
