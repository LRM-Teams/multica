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

### 2. Install the self-hosted runner on s89

Run as the existing `dev` user (already in the `docker` group). Get a token from
**Settings → Actions → Runners → New self-hosted runner** (or
`gh api -X POST repos/LRM-Teams/multica/actions/runners/registration-token`):

```bash
ssh s89
sudo -iu dev
mkdir -p ~/actions-runner && cd ~/actions-runner
curl -o runner.tar.gz -L https://github.com/actions/runner/releases/latest/download/actions-runner-linux-x64.tar.gz
tar xzf runner.tar.gz

# Labels MUST include `s89` — deploy.yml targets `runs-on: [self-hosted, s89]`.
./config.sh --url https://github.com/LRM-Teams/multica \
  --token <REGISTRATION_TOKEN> --labels s89 --unattended
```

The runner must reach GitHub through the proxy. Make the proxy env available to
the runner **service** before installing it (the runner reads `https_proxy`):

```bash
# ~/actions-runner/.env  (read by the runner at startup)
cat > .env <<'EOF'
https_proxy=http://127.0.0.1:7893
http_proxy=http://127.0.0.1:7893
no_proxy=localhost,127.0.0.1,::1,.tencentyun.com
EOF

sudo ./svc.sh install dev
sudo ./svc.sh start
```

Verify it shows **Idle** under Settings → Actions → Runners.

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

## Security note

A self-hosted runner executes whatever a triggered workflow tells it to, on a box
that also runs production (and the `leagent` / `supabase` stacks). Keep the repo's
Actions settings strict:

- **Fork pull requests:** require approval to run workflows from outside
  collaborators (Settings → Actions → General → *Fork pull request workflows*).
  `deploy.yml` only triggers on `push` to `main` and `workflow_dispatch`, never on
  `pull_request`, which keeps fork PRs off the runner — but the setting is the
  real backstop.
- Consider a dedicated low-privilege user for the runner if you later add
  workflows that run untrusted code.
