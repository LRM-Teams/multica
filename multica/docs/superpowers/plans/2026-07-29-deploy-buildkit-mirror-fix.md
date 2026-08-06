# Deploy BuildKit mirror fix

## Goal

Deploy the `dev` branch from the existing self-hosted runners without depending
on GitHub-hosted billing or direct access from Aliyun to Docker Hub.

## Evidence

- Deploy run `30433221993` did not start because the organization account could
  not use GitHub-hosted runners due to billing or spending-limit state.
- Deploy run `30432706693` reached BuildKit but timed out resolving
  `registry-1.docker.io/library/alpine:3.21`.
- On `101.200.210.144`, direct Docker Hub access timed out. The existing
  `docker.m.daocloud.io` and `docker.1ms.run` mirrors returned HTTP 401 from
  `/v2/`, confirming that both registry endpoints were reachable.
- `docker/setup-buildx-action` uses the `docker-container` driver. Its BuildKit
  daemon did not inherit the Docker Engine registry mirrors and tried Docker
  Hub directly.

## Change

- Keep Deploy `prepare` and `build` on `[self-hosted, ci]`.
- Configure the two verified Docker Hub mirrors through
  `buildkitd-config-inline`.
- Extend `scripts/selfhost-config.test.sh` so later workflow edits cannot remove
  the BuildKit mirror unnoticed.

## Verification

- [x] Reproduced the static workflow test failure on `dev`.
- [x] Static self-host configuration test passes.
- [x] Workflow syntax/lint checks pass.
- [ ] PR merged into `dev` with a merge commit.
- [ ] Deploy builds `sha-<dev>` images and the Aliyun containers run them.
