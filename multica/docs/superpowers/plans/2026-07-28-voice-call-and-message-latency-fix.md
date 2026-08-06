# Voice call and voice-message latency fix

Date: 2026-07-28

## Scope

- Restore the Beckham one-to-one realtime voice call.
- Remove avoidable queue latency from conversational voice messages.
- Keep infrastructure diagnosis separate from product-code changes.
- Submit each product change as an independent pull request.

## Step log

### 1. Baseline and deployment identity

- Local `dev`, `origin/dev`, and production images all resolve to `0c37af4c1`.
- The local worktree was clean before diagnosis.

### 2. Voice-message latency decomposition

Production timestamps for a representative message:

- Message persisted to ASR completion: 4.537 seconds.
- ASR completion to agent execution dispatch: 154.267 seconds.
- Agent execution: 21.888 seconds.
- Agent message to synthesized voice ready: 0.786 seconds.
- End-to-end: about 177.2 seconds.

The synthesis provider is not the latency source. The transcribed voice message enters the
ordinary durable agent inbox and waits behind prior long-running tasks for the same agent.

### 3. Realtime-call provider state

- `StartVoiceChat` succeeds with RTC API version `2024-12-01`.
- Recent provider sessions have task and room identifiers but never reach `connected_at`.
- No provider callback or custom LLM request reaches the backend.
- The existing frontend reduces RTC SDK failures to a generic connection error, so the exact
  browser-side error is not retained.

### 4. Public HTTPS ingress

Reproduction from outside the ECS:

- `http://leagent.me/` returns `403`, `Server: Beaver`, and
  `Non-compliance ICP Filing`.
- `https://leagent.me/` accepts TCP and closes after the TLS ClientHello without sending a
  ServerHello.
- `http://101.200.210.144:8090/` reaches Caddy and the application.

Control checks on the ECS:

- Caddy completes TLS locally with a valid `leagent.me` certificate.
- Docker publishes ports 80, 443, and 8090.
- The Caddy reverse-proxy routes for callback and LLM endpoints respond locally.

Conclusion: Alibaba Cloud's mainland ingress compliance layer intercepts the domain before
traffic reaches the ECS. Caddy, Docker, certificates, and host firewall are not the cause.
The production HTTPS endpoint requires one of:

1. Complete mainland China ICP filing for `leagent.me`.
2. Use an already-filed domain that is authorized for this ECS.
3. Move the public HTTPS ingress outside mainland China.

No host networking mutation was made because none can remove this upstream block.

## Pending product changes

1. Introduce a bounded, conversation-specific execution path for voice messages instead of
   placing them behind unrelated long-running development tasks.
2. Re-run a real call after a compliant public HTTPS endpoint is available and confirm provider
   callback, custom LLM request, RTC join, audio publish, and audio subscribe.

### 5. RTC diagnostic code

- Added a typed, optional provider code to media and controller errors.
- Only bounded numeric RTC codes cross into the UI; raw provider/browser error text remains
  hidden.
- The failure panel now renders the RTC code next to the localized recovery message.
- Regression coverage confirms `-1000` is visible while raw provider details are not.
- Focused result: 31 voice-call tests passed; `@multica/views` typecheck passed.
- React Doctor scanned the changed React surface with 0 issues.
- Full TypeScript verification passed, including 293 `views` test files / 2,816 passing
  tests and 98 `core` test files / 924 passing tests.
- The repository-wide Go command reached an unrelated macOS path-alias failure in
  `TestSecureSkillDraftBundleDirRejectsEscapes`: the valid path resolves under `/private/var`
  while the test expects `/var`. All packages before `server/pkg/agent` passed, and this
  change does not touch Go code.
