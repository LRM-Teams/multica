# Voice call secure-context error

## Goal

Tell members that browser voice calls require HTTPS when Multica is opened from
a remote HTTP origin, before loading the RTC SDK or requesting microphone
access.

## Evidence

- Production is currently reachable at `http://101.200.210.144:8090`.
- Public `leagent.me` HTTPS is intercepted before Caddy because Alibaba Cloud
  reports `Non-compliance ICP Filing`.
- Browser microphone capture is exposed only in a secure context.
- The current call panel reduces every local media failure to the same
  microphone/network message.

## Checklist

- [x] Add a failing media-session regression for an insecure browser context.
- [x] Add a failing panel regression for the dedicated member-facing message.
- [x] Reject insecure contexts before loading the RTC SDK.
- [x] Reuse the existing localized HTTPS guidance without exposing internal
  error text.
- [x] Run focused tests, typecheck, lint, and React Doctor.
- [x] Push an independent PR after the provider-code prerequisite merges.

## Boundary

This change explains the actual browser precondition. It does not bypass HTTPS,
weaken browser permissions, or treat plain HTTP as a valid production mode.
