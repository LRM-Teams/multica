# Modelfactory CLI (`mf`)

Command-line tool to manage services, jobs, and workspaces on [Modelfactory](https://modelfactory.lenovo.com).

## Install

```bash
cd /workspaces/leagent/backend/areal/multica
alias mf="PYTHONPATH=. /tmp/mf-bot/.venv/bin/python -m mf_cli.cli"
```

## Quick Start

```bash
# Login (once per 6 hours, or set MF_USERNAME/MF_PASSWORD in mf_cli/.env)
mf -u USER -p PWD login

# --- Services ---
mf list                                              # List services
mf create -m /dfs/.../model -n my-service -g A800-8  # Create new service
mf status <SERVICE_ID>                               # Check service status

# --- Service endpoint ---
mf endpoint <SERVICE_ID>                             # Show inference URL, API key, model name

# --- Leagent model registration ---
mf register <SERVICE_ID> --dry-run                   # Preview registration payload
mf register <SERVICE_ID>                             # Register service as a model in Leagent
mf edit <MODEL_NAME_OR_ID> --url https://...          # Update model's inference URL
mf edit <MODEL_NAME_OR_ID> --key sk-...              # Update model's API key
mf delete <MODEL_NAME_OR_ID> --dry-run               # Preview model deletion
mf delete <MODEL_NAME_OR_ID>                         # Delete a model from Leagent

# --- Jobs ---
mf job list                                          # List jobs
mf job create -c "sh /path/script.sh" -n my-job -g A800-8 --gpu-count 2 --cpu 32 --memory 250
mf job status <JOB_ID>                               # Check job status

# --- Workspaces ---
mf ws list                                           # List workspaces
mf ws create -n my-ws -g A800-8                     # Create workspace (default: PyTorch 2.8.0)
mf ws restart <WS_ID>                                # Restart workspace
mf ws stop <WS_ID>                                   # Stop workspace
mf ws save <WS_ID>                                   # Save workspace (persist current state as an image)
mf ws delete <WS_ID>                                 # Delete workspace
mf ws status <WS_ID>                                 # Check workspace status
```

## Commands

### Service

| Command | Description |
|---------|-------------|
| `mf create -m PATH` | Create inference service (model, A800/A100/H20, vllm) |
| `mf list` | List services |
| `mf status ID` | Service details |
| `mf endpoint ID` | Show inference endpoint (URL + API key + model name) |

### Leagent Integration

| Command | Description |
|---------|-------------|
| `mf endpoint ID` | Show service inference URL, API key, and model name |
| `mf register ID` | Register a Modelfactory service as a model in Leagent |
| `mf register ID --dry-run` | Preview what would be registered |
| `mf edit NAME_OR_ID --url URL` | Update a model's inference endpoint URL |
| `mf edit NAME_OR_ID --key KEY` | Update a model's API key |
| `mf edit NAME_OR_ID --url URL --key KEY` | Update both URL and key at once |
| `mf delete NAME_OR_ID` | Delete a model from Leagent |
| `mf delete NAME_OR_ID --dry-run` | Preview what would be deleted |

`mf register` creates a model in Leagent's admin panel (`/admin/models`) pointing
to the Modelfactory inference endpoint. It logs into Leagent's Supabase backend
and POSTs to `/admin/llm-models`.

`mf edit` updates an existing model's `api_base` (via `PATCH`) and/or `api_key`
(via `POST /rotate-key`). The model can be identified by its `model_name` or
UUID `id`.

`mf delete` permanently deletes a model from Leagent via `DELETE /admin/llm-models/{id}`.
The model can be identified by its `model_name` or UUID `id`.

**register options**

| Flag | Default | Description |
|------|---------|-------------|
| `--leagent-url` | `http://10.110.158.146:8000` | Leagent backend URL |
| `--supabase-url` | `https://wllikxifcketavoigifa.supabase.co` | Supabase URL for Leagent auth |
| `--anon-key` | (built-in) | Supabase anon key |
| `--admin-email` | `zhoujie22@lenovo.com` | Leagent admin email |
| `--admin-password` | `TpfcDebug-Reset-2026` | Leagent admin password |
| `--provider` | `openai-compatible` | Model provider identifier |
| `--display-name` | (model name) | Human-readable display name |
| `--max-output-tokens` | `16384` | Max tokens per response |
| `--context-window` | `131072` | Max context window size |
| `--capabilities` | (none) | Comma-separated capabilities |
| `--description` | (none) | Model description |
| `--dry-run` | `false` | Preview without registering |

**edit options**

| Flag | Default | Description |
|------|---------|-------------|
| `--url` | (none) | New inference endpoint URL |
| `--key` / `-k` | (none) | New API key |
| `--leagent-url` | `http://10.110.158.146:8000` | Leagent backend URL |
| `--supabase-url` | — | Supabase URL (default: from `.env`) |
| `--anon-key` | — | Supabase anon key (default: from `.env`) |
| `--admin-email` | — | Leagent admin email (default: from `.env`) |
| `--admin-password` | — | Leagent admin password (default: from `.env`) |
| `--dry-run` | `false` | Preview without editing |

**delete options**

| Flag | Default | Description |
|------|---------|-------------|
| `--leagent-url` | `http://10.110.158.146:8000` | Leagent backend URL |
| `--supabase-url` | — | Supabase URL (default: from `.env`) |
| `--anon-key` | — | Supabase anon key (default: from `.env`) |
| `--admin-email` | — | Leagent admin email (default: from `.env`) |
| `--admin-password` | — | Leagent admin password (default: from `.env`) |
| `--dry-run` | `false` | Preview without deleting |

Credentials can also be set via environment variables in `mf_cli/.env`:

```
LEAGENT_URL=http://10.110.158.146:8000
SUPABASE_URL=https://wllikxifcketavoigifa.supabase.co
SUPABASE_ANON_KEY=...
LEAGENT_ADMIN_EMAIL=zhoujie22@lenovo.com
LEAGENT_ADMIN_PASSWORD=...
```

### Job

| Command | Description |
|---------|-------------|
| `mf job create -c CMD` | Create a job with custom command |
| `mf job list` | List jobs |
| `mf job status ID` | Job details |

### Workspace

| Command | Description |
|---------|-------------|
| `mf ws create` | Create workspace (PyTorch 2.8.0 by default) |
| `mf ws list` | List workspaces |
| `mf ws status ID` | Workspace details |
| `mf ws restart ID` | Restart a running workspace |
| `mf ws stop ID` | Stop a running workspace |
| `mf ws save ID` | Save workspace state as an image |
| `mf ws delete ID` | Delete workspace |

### Workspace options

| Flag | Default | Description |
|------|---------|-------------|
| `-n`, `--name` | newWorkspace | Workspace name |
| `-g`, `--gpu` | A800-8 | GPU spec |
| `--gpu-count` | 1 | Number of GPUs |
| `--cpu` | 8 | CPU cores |
| `--memory` | 80 | Memory GiB |
| `--image` | pytorch:2.8.0 | Docker image (full registry path auto) |
| `--ssh` | false | Enable SSH |

## Architecture

```
mf_cli/
├── cli.py        # CLI entry point (argparse)
├── auth.py       # Playwright login, token caching, .env loading
├── service.py    # Inference service CRUD (pure HTTP)
├── register.py   # Leagent model registration, edit, and deletion
├── job.py        # Job CRUD (pure HTTP)
├── workspace.py  # Workspace CRUD + actions (pure HTTP)
├── config.py     # API endpoints, GPU specs, defaults
├── .env          # Credentials (MF_USERNAME, MF_PASSWORD, etc.)
└── pyproject.toml
```

- **Login**: Playwright fills the web login form → captures JWT cookie → caches to `~/.mf_cli/`
- **API**: All CRUD uses `aimaster-token-header` JWT; no browser needed after login
- **Billing**: GPU selection auto-generates price token via billing API
- **Leagent registration**: Authenticates with Supabase → registers model via Leagent admin API
