# Dev deployment target repair

## Goal

Restore `dev` deployments to the server that currently serves Multica user
traffic at `http://82.157.184.89:8090`.

## Evidence and execution log

- [x] Confirmed `origin/dev` points the deploy job at runner label `aliyun`.
- [x] Confirmed Deploy run `29974456467` executed on host
  `iZ2zegjy82jfgahlrm0w3aZ`, not s89.
- [x] Confirmed the `s89` GitHub runner is online and idle.
- [x] Confirmed `82.157.184.89` has no `sha-91374e4` images and its dev
  frontend, backend, and Caddy containers are stopped.
- [x] Traced the target change to PR #916 / commit `2d2942447`; its traffic
  cutover verification remained unchecked when the workflow target changed.
- [x] Restored the deploy job label and concurrency group to `s89` and updated
  the workflow comments to name the actual user-facing target.
- [x] Confirmed the independently managed `multica-caddy` container exists on
  s89 but is stopped; the deploy now starts that exact proxy container and
  fails explicitly if the provisioned dependency is missing.
- [x] Validated workflow syntax with actionlint v1.7.7; `git diff --check`
  also passed and the diff is limited to deploy target metadata plus this log.
- [x] Pushed `fix/deploy-dev-s89` and opened ready-for-review PR #948 into
  `dev`: <https://github.com/LRM-Teams/multica/pull/948>.
- [ ] After merge, confirm the deployment executes on s89 and that `:8090`
  serves the deployed SHA.

## Boundaries

- No server files or containers are modified directly.
- This repair does not change application code, voice behavior, or the Aliyun
  host. A future traffic cutover must change deployment and public routing as
  one verified operation.
