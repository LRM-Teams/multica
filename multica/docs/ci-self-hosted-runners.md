# Self-hosted CI runners (aliyun-144 / s89)

Team ops notes for Multica GitHub Actions self-hosted runners on the two CI
hosts. PR CI may alternate between `[self-hosted, ci]` and `ubuntu-latest`
depending on org billing; keep these runners registered either way.

## Current layout (LRM-708)

| Host | Runner dirs | Names | Labels | Process model |
|------|-------------|-------|--------|---------------|
| `101.200.210.144` (aliyun) | `/home/dev/actions-runner`, `/home/dev/actions-runner-2` | `aliyun-144`, `aliyun-144-2` | both: `ci`; primary also `aliyun` | primary: systemd; `-2`: `run.sh` + `@reboot` crontab (no passwordless sudo for `svc.sh`) |
| `82.157.184.89` (s89) | `/home/gha/actions-runner`, `/home/gha/actions-runner-2` | `s89`, `s89-2` | both: `ci`; primary also `s89` | both: systemd (`actions.runner.LRM-Teams-multica.s89{,-2}.service`) |

Deploy's host-bound job stays on `[self-hosted, aliyun]` (primary `aliyun-144`
only). Do **not** put the `aliyun` / `s89` deploy labels on the `-2` instances.

Both hosts use local egress proxy `http://127.0.0.1:7893` via each runner's
`.env` (`http_proxy` / `https_proxy`). Runner-user `~/.npmrc` points at
npmmirror when installing from CN.

## Register a second instance on the same machine

Do **not** change `ci.yml` `runs-on` for capacity — add another registered
runner with a distinct name and its own `_work` directory.

1. Copy an existing runner tree (or extract the same Actions runner tarball) into
   a sibling directory, e.g. `actions-runner-2`.
2. Create a repo registration token (Settings → Actions → Runners → New
   self-hosted runner) and configure:

   ```bash
   cd ~/actions-runner-2   # or /home/gha/actions-runner-2 on s89
   ./config.sh --url https://github.com/LRM-Teams/multica \
     --token <REGISTRATION_TOKEN> \
     --name <host>-2 \
     --labels ci \
     --work _work \
     --unattended
   ```

3. Mirror proxy env from the primary `.env`, then start:
   - **systemd** (preferred when passwordless sudo exists): `sudo ./svc.sh install <user> && sudo ./svc.sh start`
   - **nohup fallback** (aliyun-144-2 today): `nohup ./run.sh >> nohup-runner.log 2>&1 &` plus a user `@reboot` crontab line for the same command.
4. Confirm in the repo Runners UI (or `gh api repos/LRM-Teams/multica/actions/runners`) that both instances are `online` with label `ci`.

## Rollback to a single instance

If memory, disk, or Docker collide under dual jobs:

1. Stop the `-2` listener (`sudo ./svc.sh stop` or kill the `run.sh` / Listener PID).
2. Optionally remove it from GitHub: `./config.sh remove --token <REMOVE_TOKEN>`.
3. Leave the primary runner (with deploy label) untouched.
4. Record the failure mode (OOM, disk, Docker socket contention) on LRM-708 or the
   next ops issue before adding capacity again.

## Watch while dual-running

- `free -h` / `uptime` on both hosts; prefer rolling back before swap thrash (neither host has swap today).
- Disk under each `_work` plus `/` — aliyun root is tight; prune `_work/_tool` / old checkouts if >85%.
- Docker: `docker ps` / `docker stats` — Deploy and CI services must not starve each other.
