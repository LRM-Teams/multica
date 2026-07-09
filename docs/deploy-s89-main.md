# Deploying `main` to s89 on :18090 (second, isolated stack)

This documents the **server-side, one-time setup** required before
`.github/workflows/deploy-main.yml` can successfully deploy the `main` branch
alongside the existing `dev` → :8090 stack on the same s89 host.

The two stacks are fully isolated (separate Compose project, directory, Postgres
volume, and ports) so they never interfere. The main deploy deep-copies
`/data/multica/.env` into `/data/multica-main/.env` on every deploy, then
overlays only the values that must differ for the `:18090` stack. The two env
files are separate copies, not a shared file. The dev pipeline
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

### 1. Create the directory

```bash
sudo mkdir -p /data/multica-main
sudo chown "$USER":"$USER" /data/multica-main
```

Do **not** hand-maintain `/data/multica-main/.env`. The workflow copies
`/data/multica/.env` to `/data/multica-main/.env` on every deploy, so main starts
from the same env values as dev while still using its own file. This is a deep
copy, not a symlink or shared mount: editing `/data/multica-main/.env` never
mutates `/data/multica/.env`, and the next deploy refreshes the main copy from
dev.

After copying dev's env, the workflow overlays only the isolated main-stack
values via shell exports:

```env
BACKEND_PORT=18080
FRONTEND_PORT=13000
FRONTEND_ORIGIN=http://82.157.184.89:18090
MULTICA_APP_URL=http://82.157.184.89:18090
MULTICA_PUBLIC_URL=http://82.157.184.89:18090
CORS_ALLOWED_ORIGINS=http://82.157.184.89:18090
COOKIE_DOMAIN=
GOOGLE_REDIRECT_URI=http://82.157.184.89:18090/auth/callback
```

Everything else, including `APP_ENV` and `MULTICA_DEV_VERIFICATION_CODE`, comes
from dev's `/data/multica/.env`. If dev is configured with
`APP_ENV=development` and `MULTICA_DEV_VERIFICATION_CODE=888888`, then the main
`:18090` stack gets the same behavior after deploy.

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
Keep that secret equal to the dev stack's Postgres password because main starts
from a copied dev `.env` and then runs against its own `multica-main_pgdata`
volume. To give `main` its own approval gate / rollback history, create an
Environment `s89-main`, add the same `POSTGRES_PASSWORD` there, and change
`environment: s89` -> `environment: s89-main` in `deploy-main.yml`.

## Verify after first deploy

```bash
docker compose -p multica-main ps                         # 3 services up
curl -fsS http://127.0.0.1:18080/readyz && echo OK        # backend
curl -fsS http://127.0.0.1:18090/api/config && echo OK     # public via Caddy
docker compose -p multica-main logs --tail 50 backend     # migrate ok
```

dev stack remains untouched:

```bash
docker compose -p multica ps                              # still dev's containers
curl -fsS http://127.0.0.1:8080/readyz && echo OK
```
