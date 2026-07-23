# Aliyun deployment migration work log

Date: 2026-07-23

## Goal and boundary

- Deploy merged `dev` commits to `101.200.210.144` through its self-hosted GitHub Actions runner.
- Keep runtime inspection read-only over SSH. No server-side code or configuration edits.
- Preserve `/data/multica`, the existing database, the `s89` GitHub Environment as the secret source, and Caddy certificate volumes.
- Serve browsers from `https://leagent.me`; retain `:8090` for daemon compatibility and deployment verification.

## Step 1 — Inspect the new host

Completed.

- Hostname: `iZ2zegjy82jfgahlrm0w3aZ`.
- Running services: frontend, backend, PostgreSQL, and Caddy.
- Backend `/healthz` and `/readyz`: HTTP 200 from host loopback.
- Caddy `:8090/healthz`: HTTP 200 from host loopback.
- `leagent.me` and `www.leagent.me` resolve to `101.200.210.144`.
- Caddy serves both domain names and keeps `:8090` in the current runtime config.
- New-host runner service: `actions.runner.LRM-Teams-multica.aliyun-144.service`; GitHub reports runner `aliyun-144` online with label `aliyun`.
- Runner user `dev` owns `/data/multica` and belongs to the Docker group.

## Step 2 — Identify the deployment mismatch

Completed.

- Repository workflow selected `[self-hosted, s89]`, while the new host exposes `[self-hosted, aliyun]`.
- GitHub still reports a separate online `s89` runner. A recent deploy job ran there under `/home/gha/actions-runner` and failed checkout with an `EACCES` error.
- The new host was still running image `sha-c1ca522`, confirming that the current CD target did not update the migrated host.
- The repository's s89 overlay and Caddyfile were tied to `82.157.184.89`. Retargeting only the runner would have replaced the new host's domain configuration with the old IP configuration.

## Step 3 — Implement one atomic migration

Completed.

- Changed the deploy runner selector and concurrency group to `aliyun`.
- Added `docker-compose.aliyun.yml` with loopback-only backend exposure, private frontend networking, repository-managed Caddy, and canonical `leagent.me` backend URLs.
- Added `deploy/aliyun/Caddyfile` for `leagent.me`, `www.leagent.me`, WebSockets, health routing, and the compatibility `:8090` listener.
- Changed checkout, Compose, Caddy installation, and verification paths to the Aliyun-specific files.
- Changed post-deploy HTTPS probes to connect to loopback while validating the `leagent.me` certificate and redirect target.
- Kept GitHub Environment `s89` because it is the existing source of `POSTGRES_PASSWORD` and the optional speech key; no secret was copied or logged.

## Step 4 — Validate locally without starting the application

Completed.

- `git diff --check`: passed.
- `actionlint v1.7.12 .github/workflows/deploy.yml`: passed.
- `docker compose ... config -q`: passed.
- Effective Compose config verified:
  - backend published only on `127.0.0.1:8080`;
  - frontend has no published host port;
  - Caddy publishes 80/TCP, 443/TCP+UDP, and 8090/TCP;
  - application, public, frontend, and Google callback URLs use `https://leagent.me`.
- Pinned Caddy image validated `deploy/aliyun/Caddyfile`: passed.

## Step 5 — Delivery verification

The first merged cutover run exposed a second boundary and did not deploy.

- PR #970 merged as `0a16618f8`; run `29979928059` selected
  `aliyun-144` under `/home/dev/actions-runner/_work`.
- The GitHub-hosted build completed, but the Aliyun deploy job failed in
  `actions/checkout`: two fetches ended with GnuTLS recv `-110`, then a partial
  clone checkout could not fetch a promised blob after 132 seconds.
- No runtime, Caddy, migration, or readyz step ran. Build success therefore did
  not prove the cutover.

Task #668 owns the successor:

- package and validate the exact deploy files on a GitHub-hosted runner;
- upload an immutable artifact keyed by `github.sha`;
- make the Aliyun runner consume that artifact without checkout/git fetch;
- enforce `dev` ownership across the whole runner work root before and after
  artifact extraction and runtime operations;
- validate the host-local database credential before touching containers;
- require two consecutive clean/reuse deploys, ownership postflights, served
  image SHA, host-local `/readyz`, and external HTTPS before closure.
