# Voice call public-ingress diagnosis

## Goal

Provide a public HTTPS origin that both browsers and Volcengine RTC can reach
before accepting the real-time voice-call feature as usable.

## Evidence collected on 2026-07-29

- [x] Confirm the deployed Caddy container listens on host ports `80`, `443`,
  and `8090`.
- [x] Confirm the Caddy loopback HTTPS route returns `200` for `/healthz`.
- [x] Confirm `http://101.200.210.144:8090/healthz` is publicly reachable and
  returns `200`.
- [x] Confirm public HTTP requests for `leagent.me` do not reach Caddy. Alibaba
  Cloud returns `403`, `Server: Beaver`, and an HTML page titled
  `Non-compliance ICP Filing`.
- [x] Confirm public HTTPS requests for `leagent.me` fail during the TLS
  handshake. An independent SSL Labs probe reports `Failed to communicate with
  the secure server`.
- [x] Confirm Caddy received no `/api/voice-calls/callback` request during the
  preceding 72 hours.
- [x] Confirm the deployed backend starts with the Volcengine voice-call
  integration enabled.

## Conclusion

The public `leagent.me` traffic is intercepted before it reaches Caddy because
the Alibaba Cloud ingress rejects the domain for ICP non-compliance. This is an
infrastructure and domain-registration condition, not an application routing
or RTC SDK defect.

The direct `http://101.200.210.144:8090` entry cannot replace the HTTPS origin
for browser calls. Browser microphone capture is a secure-context API, so a
normal remote HTTP origin cannot provide the media stream required by the RTC
client.

The unavailable public callback URL also prevents Multica from receiving the
provider's asynchronous task states, subtitles, and startup errors.

## Required infrastructure action

Choose one:

1. Complete ICP filing and Alibaba Cloud access registration for `leagent.me`
   on this ECS instance, then continue using
   `https://leagent.me/api/voice-calls/callback`.
2. Move the public HTTPS entry to infrastructure that does not require this ICP
   registration, while keeping a valid certificate and forwarding both the web
   application and `/api/voice-calls/callback`.

Do not point production browsers at plain HTTP and do not replace the provider
callback with an unverified temporary tunnel.

## Acceptance checks

- [ ] An external `curl https://leagent.me/healthz` returns `200` from Caddy.
- [ ] An external TLS checker completes the handshake for `leagent.me`.
- [ ] The browser reports `window.isSecureContext === true`.
- [ ] Starting a call produces a POST to `/api/voice-calls/callback`.
- [ ] A call session records `connected_at`.
- [ ] A completed test call records non-zero input and output audio duration.
- [ ] The agent plays its configured welcome message before the member's first
  turn.

## Related code changes

- PR #1313 exposes a bounded RTC diagnostic code.
- PR #1345 plays local ringback while the call is connecting.
- PR #1371 preserves SDK enum and rejected-promise error codes.

These changes improve feedback and diagnosis. They do not bypass the HTTPS
requirement.
