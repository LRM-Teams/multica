# Voice transcription status UI work log

Date: 2026-07-23

## Goal and boundary

- Keep the voice bubble as the primary message while server ASR is pending or
  failed.
- Show a transcript action only when canonical transcript text is available.
- Do not disable original recording playback when transcription fails.

## Step 1 — Pending state

Completed.

- Pending recordings show a compact `转写中` status beside the bubble.
- The status is a polite live region and its spinner honors reduced motion.
- No transcript button or empty transcript panel is rendered.

## Step 2 — Failed state

Completed.

- Failed recordings show `无法转文字`.
- The failure is scoped to transcription; the original audio control remains
  enabled.

## Step 3 — Verification

Completed.

- `pnpm --filter @multica/views typecheck`: passed.
- Focused voice bubble regression: 1 file and 11 tests passed.
- React Doctor initially flagged both generic `role="status"` elements; they
  were replaced with native `output` elements. The second scan reports 0
  issues.
- Web Interface Guidelines review found no remaining changed-line findings.
