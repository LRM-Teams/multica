# Continuous deployment to s89

This documents the CI/CD pipeline defined in `.github/workflows/deploy.yml`:
**merge to `main` → build images on GitHub → a self-hosted runner on s89 pulls
and restarts the stack.**

## Why it is shaped this way

s89 is a Tencent Cloud server in **Beijing, behind the Great Firewall**
(`82.157.184.89`). Its outbound traffic reaches GitHub/GHCR only through a local
proxy on `127.0.0.1:7893`; the Docker daemon is already configured to pull
through it (`/etc/systemd/system/docker.service.d/http-proxy.conf`).

Inbound SSH from GitHub-hosted runners into a mainland-China IP is the
throttled/blocked direction across the GFW, so the pipeline **never connects
into s89**. Everything rides the outbound direction that already works:

1. GitHub-hosted runners (outside China) build the amd64 images and push to GHCR.
2. A **self-hosted runner on s89** long-polls GitHub *outbound* through the proxy,
   then runs `docker compose pull && up -d` locally. No inbound port, no public
   SSH, no security-group changes.

The proxy on `:7893` is a hard dependency of the whole box — if it is down,
deploys (and manual `docker compose pull`) fail. Keep it running as a service.

## Pipeline at a glance

| Stage | Where it runs | What it does |
|---|---|---|
| `build` | GitHub-hosted `ubuntu-latest` | Build `Dockerfile` + `Dockerfile.web` (amd64), push `ghcr.io/lrm-teams/multica-{backend,web}` tagged `:latest` and `:sha-<short>` |
| `deploy` | self-hosted runner on s89 (label `s89`) | `docker login ghcr` (job token), `compose pull/up -d backend frontend`, then poll `http://127.0.0.1:8080/readyz` |

Postgres and Caddy are left untouched; only `backend` + `frontend` roll. The
backend runs `migrate up` on startup (`docker/entrypoint.sh`), so DB migrations
apply automatically on each deploy.

## One-time setup

### 1. Branch protection = the test gate

`deploy.yml` does **not** re-run tests. Make `main` require the CI workflow to be
green before merge, so anything reaching `main` is already tested:

> GitHub → repo **Settings → Branches → Add branch ruleset** for `main` →
> require a pull request, and **Require status checks** → select the `CI` checks.

### 2. Self-hosted runner on s89 (already installed — recorded here for rebuild)

The runner runs as a **dedicated low-privilege `gha` user** (never `dev` or root),
pinned + hash-verified, reaching GitHub outbound through the proxy. These steps
reproduce the install from scratch:

```bash
# --- dedicated user, docker access, least-privilege secret read ---
sudo useradd -m -s /bin/bash gha
sudo usermod -aG docker gha                     # needed for `docker compose`
sudo setfacl -m u:gha:r /data/multica/.env      # read ONLY the prod secrets it needs

# --- download a pinned, hash-verified runner (as gha, via the proxy) ---
sudo -u gha env https_proxy=http://127.0.0.1:7893 http_proxy=http://127.0.0.1:7893 bash -s <<'EOF'
set -euo pipefail
mkdir -p ~/actions-runner && cd ~/actions-runner
V=2.335.1
curl -fsSL -o runner.tar.gz "https://github.com/actions/runner/releases/download/v${V}/actions-runner-linux-x64-${V}.tar.gz"
echo "4ef2f25285f0ae4477f1fe1e346db76d2f3ebf03824e2ddd1973a2819bf6c8cf  runner.tar.gz" | sha256sum -c -
tar xzf runner.tar.gz && rm -f runner.tar.gz
EOF

# --- proxy for the SERVICE (systemd does NOT inherit shell env) ---
sudo -u gha tee /home/gha/actions-runner/.env >/dev/null <<'EOF'
https_proxy=http://127.0.0.1:7893
http_proxy=http://127.0.0.1:7893
no_proxy=localhost,127.0.0.1,::1,.tencentyun.com,.tencent.com
EOF

# --- .NET native deps: TencentOS (RHEL9-like) isn't detected by the bundled
#     installdependencies.sh, so install them directly via dnf (Tencent mirror) ---
sudo dnf install -y libicu krb5-libs zlib openssl-libs

# --- register (token is single-use, ~1h TTL) ---
TOKEN=$(gh api -X POST repos/LRM-Teams/multica/actions/runners/registration-token --jq .token)
sudo -u gha env https_proxy=http://127.0.0.1:7893 http_proxy=http://127.0.0.1:7893 \
  bash -c "cd ~/actions-runner && ./config.sh --url https://github.com/LRM-Teams/multica \
           --token $TOKEN --name s89 --labels s89 --unattended --replace"

# --- run as a boot-enabled systemd service, as gha ---
sudo bash -c 'cd /home/gha/actions-runner && ./svc.sh install gha && ./svc.sh start'
```

Verify it is `online` with the right labels:

```bash
gh api repos/LRM-Teams/multica/actions/runners \
  --jq '.runners[] | {name,status,labels:[.labels[].name]}'
# → {"name":"s89","status":"online","labels":["self-hosted","Linux","X64","s89"]}
```

The labels must include `s89` so `runs-on: [self-hosted, s89]` matches.

### 3. Point the server's `.env` at the fork images (recommended)

The workflow overrides the image refs at deploy time, so automated deploys work
regardless. But update `/data/multica/.env` so manual `docker compose` commands
on the box also use the fork's images and stay consistent:

```dotenv
MULTICA_BACKEND_IMAGE=ghcr.io/lrm-teams/multica-backend
MULTICA_WEB_IMAGE=ghcr.io/lrm-teams/multica-web
MULTICA_IMAGE_TAG=latest
```

### 4. GHCR access

The fork's packages are private by default. The deploy job logs in per-run with
the ephemeral job token (`packages: read`), so **no long-lived credential lives
on the box**. For manual pulls on s89, log in once with a PAT
(`read:packages`): `echo <PAT> | docker login ghcr.io -u <user> --password-stdin`.

## Day-to-day

- **Deploy:** merge a PR to `main`. Watch it under the repo's **Actions** tab.
- **Rollback / redeploy a known build:** Actions → **Deploy** → **Run workflow** →
  set `image_tag` to a previous tag (e.g. `sha-abc1234`). This skips building and
  redeploys that image. Find tags under the repo's **Packages**.
- **Health:** the deploy fails loudly if `/readyz` doesn't pass within 60s, and
  dumps the last 80 lines of `multica-backend-1` logs.

## Security

A self-hosted runner executes whatever a triggered workflow tells it to, on a box
that also runs production (plus the `leagent` / `supabase` stacks).

- **The repo MUST stay private.** GitHub explicitly recommends against self-hosted
  runners on public repos: a fork PR can change a workflow to
  `runs-on: [self-hosted, s89]` and run arbitrary code on the box. The repo was made
  **private** for exactly this reason — do not flip it back to public while the s89
  runner is registered.
- **Dedicated low-privilege user.** The runner runs as `gha` (not `dev`/root), with an
  ACL granting read on only `/data/multica/.env`. Caveat: `gha` is in the `docker`
  group, which is root-equivalent (a container can mount the host), so the user split
  is isolation/hygiene — the real boundary is the private repo. For a hard boundary,
  use rootless Docker or a dedicated runner host.
- `deploy.yml` triggers only on `push` to `main` and `workflow_dispatch`, never
  `pull_request`.
- *Optional hardening:* ephemeral runners (`--ephemeral`, fresh per job) if you later
  add workflows that run untrusted code.
