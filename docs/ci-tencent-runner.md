# Self-hosted CI runners (`[self-hosted, ci]`)

> **Authoritative roles (Frank, 2026-07-29; updated same day — Aliyun also CI):**
>
> | Role | Host / labels |
> | --- | --- |
> | **PR CI** (`ci.yml`) | Any runner with labels `[self-hosted, ci]`. **Aliyun** `101.200.210.144` **must** carry `ci` in addition to `aliyun`. Optional extra Tencent CI box may register the same `ci` label and share the queue. |
> | **Shared Multica `dev` deploy** | Aliyun `101.200.210.144` (`leagent.me`), deploy job stays `runs-on: [self-hosted, aliyun]` — see `deploy.yml` / `docs/deploy-s89.md` |
>
> YAML does **not** need a second `runs-on` variant: GitHub matches **all** listed labels. Aliyun keeps `aliyun` for Deploy and adds `ci` so it can also pick up PR CI. When CI and Deploy share Aliyun, expect CPU contention during large frontend suites — prefer not to start a deploy mid-CI, or add a dedicated Tencent `ci` box later for relief.

`.github/workflows/ci.yml` uses `runs-on: [self-hosted, ci]`. Until at least one runner with those labels is online and idle, PR checks will queue.

macOS installer jobs are out of the PR CI gate (Frank 2026-07-29); do not reintroduce a GitHub-hosted macOS job into `ci.yml`.

## Immediate cutover: add `ci` on Aliyun

Aliyun runner is already registered for Deploy. To also serve CI:

1. GitHub → **LRM-Teams/multica → Settings → Actions → Runners** → open the Aliyun runner (`aliyun-144` / labels include `aliyun`).
2. **Add label** `ci` (keep existing `self-hosted`, `aliyun`, `Linux`, `X64`).
3. Confirm the runner is **Idle**, then re-run a PR check (or open a tiny workflow-touching PR). `CI / frontend` and `CI / backend` should list the Aliyun runner name — not `ubuntu-latest`.
4. Confirm the runner user can use Docker (`docker info`); backend CI needs service containers for Postgres/Redis. Deploy already uses Docker on this host.

No workflow diff is required for this step if `ci.yml` already says `runs-on: [self-hosted, ci]` (merged via LRM-701 / PR #1393).

## Optional: dedicated Tencent CI box

If Aliyun is too busy with Deploy, register a second Linux runner (Tencent Cloud is fine) with labels `self-hosted` + `ci` only. It joins the same queue; GitHub assigns to any idle match.

### Machine sizing (dedicated box)

| Workload | Recommendation |
| --- | --- |
| frontend + backend in parallel | **8 vCPU / 16 GiB** preferred; 4c/8G is the floor |
| Disk | ≥ 80 GiB SSD for Docker images, pnpm store, Go module cache, Turbo/Next caches |
| OS | Ubuntu 22.04 or 24.04 LTS |

### Host packages (dedicated box)

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

### Register a new runner

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

## Cache / work directory

| Path | Purpose |
| --- | --- |
| Runner `_work` | Job workspaces (leave on a large local disk) |
| pnpm store | Prefer a stable path under the runner user home so installs stay warm |
| Go module cache | `$HOME/go/pkg/mod` under the runner user |
| Docker image cache | Leave Docker's data-root on the same SSD |

Do **not** run agent/human git worktrees under the runner `_work/**` tree (same rule as Aliyun deploy ownership in `docs/engineering-principles.md`). On Aliyun, CI checkouts and Deploy artifact worktrees must stay under the runner user’s `_work` only — never reuse that tree as an interactive git worktree.

## Outbound network

The runner must reach:

- `github.com` / `api.github.com` (job lease, checkout)
- `*.actions.githubusercontent.com` (action downloads)
- npm registry / Go module proxies / Docker Hub (or your mirrors)

If the host sits behind a corporate proxy, configure it for the **runner service user** (environment in the systemd unit), not only interactive shells. See `docs/deploy-s89.md` for the historical s89 proxy triage pattern.

## What remains on GitHub-hosted runners

The web-only policy intentionally retains only the release paths needed for the
served Linux amd64 Web/backend images and Helm chart:

- `release.yml` Linux amd64 Web/backend image builds and Helm chart publication

`deploy.yml` prepares and builds images on `[self-hosted, ci]`; its final
production deployment remains `[self-hosted, aliyun]`. Mobile, desktop,
cross-platform CLI, and Linux ARM workflow lanes are disabled by the web-only
policy.

## Verification checklist

- [ ] Aliyun runner labels include `self-hosted` + `aliyun` + **`ci`**
- [ ] `docker info` works as the Aliyun runner user
- [ ] A PR into `dev` runs `CI / frontend` and `CI / backend` on a self-hosted runner (Aliyun and/or Tencent `ci`), not `ubuntu-latest`
- [ ] Org Actions hosted-minute burn for `CI` drops after cutover
- [ ] (Optional) Dedicated Tencent `ci` runner online for load relief

## CI cache

The Turbo cache is published on `dev` by the `push` trigger in `ci.yml`; pull
requests restore it via the `-dev-` restore-key. There is deliberately no
ref-scoped restore-key: it would shadow the dev entry with the PR's own older
(or cancelled-run) cache. See PR #1448.

### Verifying a cache-restore change

A pull request's **first** run proves nothing: with no ref-scoped entry saved
yet, it falls through to the `dev` key even with a broken configuration. Check
the **second** run of the same branch, and read the restored key before the hit
count — `Cache restored from key:` fails earlier and louder than `Cached: N`.

Note that `github.ref_name` on a `pull_request` event is `<number>/merge`, so
the entry to look for is `…-turbo-CI-<number>/merge-…`, not the branch name.
