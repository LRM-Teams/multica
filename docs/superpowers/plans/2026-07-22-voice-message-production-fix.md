# Voice message production fix

Overall status: application changes are complete and published. Real-browser microphone capture remains blocked until the deployment has a trusted HTTPS entrypoint.

## Goal

Fix the deployed voice-message experience without changing providers or hiding failures: place recording beside Send, make recording available through a browser-secure origin, render Agent voice replies as playable duration-bearing chat bubbles, and remove the reported noisy playback.

## Step 1 — Reproduce and identify the production failures

Status: complete

Evidence:

- The deployed user traffic reaches `http://82.157.184.89:8090`; Caddy access records contain that exact Origin and Referer from Chrome/Edge clients.
- Browsers do not expose `navigator.mediaDevices.getUserMedia` on that non-secure origin. The current component merges this condition with missing `MediaRecorder` and `AudioContext` into the generic “browser unsupported” message before asking for permission.
- The server has a valid `leagent.me` certificate and serves the app over HTTPS from inside the host, but external TLS connections to port 443 currently close during the handshake. The host listener, Docker mapping, and host firewall accept 443, so the remaining external-network/security-group boundary must be fixed before the secure origin is usable.
- The microphone is rendered in the left leading-actions container with Attach, while Send is a separate right-side child. The current test only checks that both are somewhere in the action row, so it cannot detect the reported placement error.
- Agent voice replies currently render a small labelled control below ordinary message text. They do not render a chat bubble or expose media duration.
- TTS requests MP3 at 24 kHz and the client decodes the returned bytes with `AudioContext`. A live request using the production account returned a valid ID3-tagged, 24 kHz mono MP3 (2.04 seconds), so the provider output is not raw PCM mislabeled as MP3; the reported noise occurs after the provider boundary.

## Step 2 — Add regression tests

Status: complete

Evidence:

- Added a Composer DOM assertion that the microphone and Send are siblings in one submit-actions group; the old layout has no such group.
- Added a voice-reply assertion for a playable bubble that shows the decoded duration without using the accessible action label as visible copy.
- Changed the handler contract test to require 24 kHz PCM wrapped in a RIFF/WAV container, plus an explicit duration header, instead of returning MP3 bytes for browser-specific decoding.
- Added a typed-client regression requiring the new `audio/wav` response contract.
- The regression set failed against the old implementation at the intended seams: no submit-actions group, no voice-bubble marker/duration, and rejection of `audio/wav`.

## Step 3 — Implement layout, secure-origin handling, voice bubble, and audio fix

Status: complete

Evidence:

- Moved the shared microphone out of the leading attachment lane into a dedicated submit-actions group immediately before Send. The same Composer still serves groups, DMs, and threads.
- Split insecure-origin detection from missing browser APIs. HTTP now reports that HTTPS is required; permission denial and actual API absence keep their distinct messages.
- Changed TTS delivery from provider MP3 to provider PCM wrapped by the backend as a 24 kHz, mono, signed-16-bit RIFF/WAV response. The handler supplies exact container fields and duration; the browser decodes a self-describing stream.
- Replaced the visible text-labelled replay pill with a clickable voice bubble using the existing Lucide audio-wave asset, play/stop/loading/retry states, and the duration measured from the decoded audio. The accessible action label remains available to assistive technology.
- Updated the original voice transport record and the project engineering principles so future work does not claim that HTTP `:8090` can validate microphone capture or that the TTS endpoint still returns MP3.

External condition:

- Browser recording still requires a reachable HTTPS entrypoint. `leagent.me` is blocked at the Tencent public edge before Caddy; application deployment cannot remove that block. A filed/approved domain or an automatically renewed public IP certificate is required.

## Step 4 — Verify locally and review production-facing conditions

Status: complete

Evidence:

- Six affected Views files passed 88 tests with two existing skips; Core API passed 46 tests.
- Full monorepo TypeScript checking passed. Full lint passed with zero errors and seven warnings in unchanged files.
- Agent CLI, runtime instructions, message-part validation, and Doubao transport packages passed their Go tests.
- Four voice handler tests passed against a new isolated PostgreSQL database. The temporary database was removed; the inconsistent pre-existing local handler database was left unchanged.
- The opt-in live provider test passed with the configured production account: TTS returned 63,232 PCM bytes and ASR transcribed `你好，我是贝克汉姆。`.
- Server inspection remained read-only. The running backend logged no voice-provider failures; its image is the merged voice feature SHA.
- `leagent.me` remains unusable externally because Tencent redirects HTTP to its web-block page and closes HTTPS before Caddy. The application now reports this condition accurately, but microphone capture cannot pass a real-browser deployment check until a trusted HTTPS entrypoint exists.

CI follow-up:

- The first final-SHA frontend job stopped at React Doctor before the normal build because `voiceCaptureUnavailableReason` was exported from the React component file. This would force Fast Refresh to reload component state during development.
- Moved the pure capability detector and its tests into `channels/lib/voice-capture.ts`; the component file now exports only the component/type contract expected by the existing codebase.

## Step 5 — Push and open a PR against `dev`

Status: complete

Evidence:

- Committed the scoped fix as `54d834ec9` on `fix/voice-message-production` and pushed it to `origin`.
- Opened PR #896 against `dev` and marked it ready for review: `fix(channels): repair production voice messages`.
- The PR states that the application fixes are verified while browser recording remains conditional on a trusted HTTPS entrypoint; deployment will run only after the user merges the PR.
