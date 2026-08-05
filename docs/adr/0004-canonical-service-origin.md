# Use leagent.me as the canonical service origin

The hosted Machine Service uses `https://leagent.me` as its canonical
`serverUrl`. HTTP API and WebSocket endpoints are derived from this origin;
`api.leagent.me` is not a separate server identity or attachment boundary. A
single canonical origin prevents URL aliases from creating duplicate local
identity, authentication, or Workspace Binding state. A dedicated API hostname
would add DNS, TLS, CORS, cookie, and configuration surfaces without creating a
useful product boundary.
