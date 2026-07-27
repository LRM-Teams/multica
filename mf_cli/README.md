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

`mf register` creates a model in Leagent's admin panel (`/admin/models`) pointing
to the Modelfactory inference endpoint. It logs into Leagent's Supabase backend
and POSTs to `/admin/llm-models`.

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
├── register.py   # Leagent model registration
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
