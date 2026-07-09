# Deploying `main` to s89 on :18090 (second, isolated stack)

This documents the **server-side, one-time setup** required before
`.github/workflows/deploy-main.yml` can successfully deploy the `main` branch
alongside the existing `dev` → :8090 stack on the same s89 host.

The two stacks are fully isolated (separate Compose project, directory, Postgres
volume, and ports) so they never interfere. The dev pipeline
(`.github/workflows/deploy.yml`) is unchanged.

## How it is wired

| | dev (existing) | main (new) |
|---|---|---|
| Compose project | `multica` | `multica-main` |
| Server directory | `/data/multica` | `/data/multica-main` |
| Postgres volume | `multica_pgdata` | `multica-main_pgdata` |
| Backend host port | `8080` | `18080` |
| Frontend host port | `3000` | `13000` |
| Public port (via Caddy) | `8090` | `18090` |
| Concurrency group | `deploy-s89` | `deploy-s89-main` |
| Trigger | PR merged → `dev` | PR merged → `main` |

## One-time server setup (run on s89 as the deploy user)

### 1. Create the directory and `.env`

```bash
sudo mkdir -p /data/multica-main
sudo chown "$USER":"$USER" /data/multica-main

cp /data/multica/.env /data/multica-main/.env
```

Then edit `/data/multica-main/.env` and **change at least**:

- `JWT_SECRET` — generate a new one (`openssl rand -hex 32`). Do NOT reuse dev's.
- `POSTGRES_PASSWORD` — can stay the same value as dev's; it only seeds this
  stack's OWN fresh postgres volume. The deploy injects the `s89` Environment
  secret `POSTGRES_PASSWORD` and it must match whatever you set here for the
  backend's `DATABASE_URL`. Simplest: keep it equal to dev's.
- `POSTGRES_DB` / `POSTGRES_USER` — leave as `multica` is fine (separate volume).

The deploy workflow **forces** `BACKEND_PORT=18080` and `FRONTEND_PORT=13000`
via env vars, so those in `.env` are informational; set them too for clarity:

```env
BACKEND_PORT=18080
FRONTEND_PORT=13000
```

### 2. First-run bring up the isolated stack

The first deploy must create the `multica-main` postgres volume + container and
run the initial migration. Either:

- **(preferred)** just merge the first `main` PR — the workflow's
  `compose up -d` will create everything on first run, **or**
- **(manual)** bring it up once yourself to validate:

  ```bash
  cd /data/multica-main
  docker compose -p multica-main --project-directory /data/multica-main \
    --env-file /data/multica-main/.env \
    -f <path-to>/docker-compose.selfhost.yml up -d
  docker compose -p multica-main ... logs -f backend   # watch `migrate up`
  curl -fsS http://127.0.0.1:18080/readyz
  ```

### 3. Add a Caddy block for `:18090`

s89 runs a standalone Caddy that fronts `:8090` for the dev stack. Add a mirror
block for `:18090` pointing at the **main** host ports (`13000` frontend,
`18080` backend). Adapt the exact routing from your existing `:8090` block.

Example (adjust to match how your `:8090` block is written):

```caddyfile
:18090 {
    # Frontend (Next.js) on the main stack's host port
    reverse_proxy 127.0.0.1:13000

    # If your :8090 block routes /api, /readyz, /ws etc. to the backend,
    # mirror those here against the main backend port:
    # handle /api/* { reverse_proxy 127.0.0.1:18080 }
    # handle /readyz { reverse_proxy 127.0.0.1:18080 }
    # handle /ws    { reverse_proxy 127.0.0.1:18080 }
}
```

Reload Caddy: `sudo systemctl reload caddy` (or `docker exec ... caddy reload`
if Caddy runs in a container).

> If `:8090` is **not** fronted by Caddy but is instead the raw `BACKEND_PORT`
> in `/data/multica/.env`, then `:18090` is just the main stack's backend port —
> set the workflow / `.env` accordingly. Inspect `docker ps` and the existing
> `/data/multica/.env` to confirm which model s89 uses.

### 4. (Optional) dedicated GitHub Environment

The workflow reuses the `s89` Environment (for `secrets.POSTGRES_PASSWORD`).
To give `main` its own approval gate / rollback history, create an Environment
`Actions secrets/Variables → Environments → s89-main`, add `POSTGRES_PASSWORD`
there, and change `environment: s89` → `environment: s89-main` in
`deploy-main.yml`.

## Verify after first deploy

```bash
docker compose -p multica-main ps                         # 3 services up
curl -fsS http://127.0.0.1:18080/readyz && echo OK        # backend
curl -fsS http://127.0.0.1:18090/ && echo OK              # public via Caddy
docker compose -p multica-main logs --tail 50 backend     # migrate ok
```

dev stack remains untouched:

```bash
docker compose -p multica ps                              # still dev's containers
curl -fsS http://127.0.0.1:8080/readyz && echo OK
```
