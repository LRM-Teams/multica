# Split hosted web and API origins by environment

The hosted product uses separate public coordinates:

- Web application: `MULTICA_APP_URL` (`https://www.leagent.me` in production)
- HTTP API and authentication: `MULTICA_API_URL`
  (`https://api.leagent.me` in production)
- WebSocket: `MULTICA_WS_URL` (`wss://api.leagent.me/ws` in production)
- Release artifacts: `https://cdn.leagent.me/computer`

These values are deployment configuration, not business-layer constants.
Test and staging deployments provide their own host/URL values through the
environment. Next.js public values are injected at build time so a test web
bundle cannot accidentally call the production API; backend and Caddy values
are injected at runtime.

Browser API requests use credentials and are limited by an explicit CORS
allowlist. The shared parent-domain cookie is configured only for hosted
environments where the web and API are sibling subdomains. Self-hosted and LAN
deployments leave `COOKIE_DOMAIN` empty and keep their existing single-origin
behavior.

The bare production domain redirects browser navigation to the web origin.
Legacy API/WebSocket proxy paths remain temporarily available there for older
installed daemons, but all newly built clients persist and call the dedicated
API origin. This supersedes the earlier single-origin decision.

A sibling-subdomain test environment uses the same deployment contract with a
different variable set, for example:

```dotenv
MULTICA_ROOT_HOST=test.leagent.me
MULTICA_APP_HOST=www.test.leagent.me
MULTICA_API_HOST=api.test.leagent.me
MULTICA_APP_URL=https://www.test.leagent.me
MULTICA_API_URL=https://api.test.leagent.me
MULTICA_WS_URL=wss://api.test.leagent.me/ws
MULTICA_CORS_ALLOWED_ORIGINS=https://www.test.leagent.me
MULTICA_COOKIE_DOMAIN=test.leagent.me
MULTICA_GOOGLE_REDIRECT_URI=https://www.test.leagent.me/auth/callback
```

CI maps `MULTICA_APP_URL`, `MULTICA_API_URL`, and `MULTICA_WS_URL` to the
matching `NEXT_PUBLIC_*` build arguments. Each environment therefore gets its
own web image, while the same source and Compose/Caddy templates are reused.
