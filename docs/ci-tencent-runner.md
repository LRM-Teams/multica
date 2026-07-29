# Tencent Cloud CI runner (self-hosted)

> **Authoritative roles (Frank, 2026-07-29):**
>
> | Role | Host / labels |
> | --- | --- |
> | **PR CI** (`ci.yml`, `mobile-verify.yml`) | Tencent Cloud dedicated runner, labels `[self-hosted, ci]` |
> | **Shared Multica `dev` deploy** | Aliyun `101.200.210.144` (`leagent.me`), labels `[self-hosted, aliyun]` — see `deploy.yml` / `docs/deploy-s89.md` |
>
> Deploy and CI must **not** share one machine: Deploy stays on Aliyun; CI gets its own box (same Tencent cloud as historical s89 is fine). GitHub only orchestrates; compute stays on the self-hosted runner (private repo self-hosted minutes are not billed as hosted Actions minutes).

`.github/workflows/ci.yml` and `mobile-verify.yml` use `runs-on: [self-hosted, ci]`. Until a runner with those labels is online and idle, PR checks will queue.

macOS installer jobs are out of the PR CI gate (Frank 2026-07-29); do not reintroduce a GitHub-hosted macOS job into `ci.yml`.

## Machine sizing

| Workload | Recommendation |
| --- | --- |
| frontend + backend in parallel | **8 vCPU / 16 GiB** preferred; 4c/8G is the floor |
| Disk | ≥ 80 GiB SSD for Docker images, pnpm store, Go module cache, Turbo/Next caches |
| OS | Ubuntu 22.04 or 24.04 LTS |

## Host packages

Install as root (or with sudo) before registering the runner:

```bash
sudo apt-get update
sudo apt-get install -y git curl ca-certificates build-essential \
  docker.io docker-compose-v2

# Node 22 + pnpm (corepack) — matches ci.yml setup-node
curl -fsSL https://deb.nodesource.com/setup_22.x | sudo -E bash -
sudo apt-get install -y nodejs
sudo corepack enable
sudo corepack prepare pnpm@10.28.2 --activate

# Go 1.26.x — matches ci.yml setup-go
# (install from https://go.dev/dl/ or your preferred apt source)

sudo usermod -aG docker "$(id -un)"   # or the dedicated runner user
sudo systemctl enable --now docker
```

Backend CI starts Postgres (`pgvector/pgvector:pg17`) and Redis via GitHub Actions
**service containers**. The runner user must be able to talk to the Docker daemon;
without Docker, the backend job fails at job setup.

## Register the GitHub Actions runner

1. In GitHub: **LRM-Teams/multica → Settings → Actions → Runners → New self-hosted runner**.
2. Follow the Linux x64 download/config steps as a dedicated user (recommended home: `/home/gha/actions-runner` or `/home/ci/actions-runner`).
3. When `./config.sh` asks for labels, set at least: `self-hosted`, `ci` (and keep the default `Linux` / `X64` if offered).
4. Install and start the service:

```bash
sudo ./svc.sh install
sudo ./svc.sh start
sudo ./svc.sh status
```

5. Confirm the runner shows **Idle** with labels including `ci` on the repo Runners page.
6. Open a no-op PR touching `ci.yml` or re-run an existing PR check; both `frontend` and `backend` should land on this runner (not `ubuntu-latest`).

## Cache / work directory

| Path | Purpose |
| --- | --- |
| Runner `_work` | Job workspaces (leave on a large local disk) |
| pnpm store | Prefer a stable path under the runner user home so installs stay warm |
| Go module cache | `$HOME/go/pkg/mod` under the runner user |
| Docker image cache | Leave Docker's data-root on the same SSD |

Do **not** run agent/human git worktrees under the runner `_work/**` tree (same rule as Aliyun deploy ownership in `docs/engineering-principles.md`).

## Outbound network

The runner must reach:

- `github.com` / `api.github.com` (job lease, checkout)
- `*.actions.githubusercontent.com` (action downloads)
- npm registry / Go module proxies / Docker Hub (or your mirrors)

If the host sits behind a corporate proxy, configure it for the **runner service user** (environment in the systemd unit), not only interactive shells. See `docs/deploy-s89.md` for the historical s89 proxy triage pattern.

## What stays on GitHub-hosted runners

Intentionally **not** moved by LRM-701:

- `deploy.yml` **prepare / build-image** jobs (still package on hosted runners; only the final deploy step runs on `[self-hosted, aliyun]`)
- `release.yml` multi-arch image builds
- `desktop-smoke.yml` (manual `workflow_dispatch`; Windows matrix still needs a Windows runner)

## Verification checklist

- [ ] Runner online with labels `self-hosted` + `ci`
- [ ] `docker info` works as the runner user
- [ ] `node -v` → 22.x, `pnpm -v` → 10.28.x, `go version` → 1.26.x
- [ ] A PR into `dev` runs `CI / frontend` and `CI / backend` on the Tencent runner
- [ ] Org Actions hosted-minute burn for `CI` drops after cutover
