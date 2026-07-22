# Modelfactory CLI (`mf`)

Command-line tool to manage inference services and jobs on [Modelfactory](https://modelfactory.lenovo.com).

## Install

```bash
cd /workspaces/leagent/backend/areal/mf_cli
# Use the existing playwright venv:
export VENV=/tmp/mf-bot/.venv/bin/python
alias mf="PYTHONPATH=/workspaces/leagent/backend/areal $VENV -m mf_cli.cli"
```

## Quick Start

```bash
# Login (once per 6 hours)
mf -u zhoujie22 -p "PWD" login

# List services
mf list

# Create a service
mf create -m /dfs/share-groups/letrain/zhoujie/AReaL-main/Qwen3.5-9B -n Qwen3.5-9B -g A800-8

# List jobs
mf job list

# Create a job
mf job create \
  -c "sh /dfs/share-groups/letrain/zhoujie/le-agent-dev_new/db_bridge/run_dev.sh" \
  -n le-agent-dev \
  -g A800-8 --gpu-count 2 \
  --cpu 32 --memory 250
```

## Commands

### Service Management

| Command | Description |
|---------|-------------|
| `mf create -m PATH` | Create inference service |
| `mf list` | List services |
| `mf status ID` | Service details |

### Job Management

| Command | Description |
|---------|-------------|
| `mf job create -c CMD` | Create a job |
| `mf job list` | List jobs |
| `mf job status ID` | Job details |

### Options

| Flag | Description |
|------|-------------|
| `-u`, `--username` | Modelfactory username |
| `-p`, `--password` | Modelfactory password |
| `-v`, `--visible` | Show browser during login |
| `-g`, `--gpu` | GPU spec (A800-8, A100-8, H20-8, ...) |
| `-n`, `--name` | Resource name/alias |
| `-c`, `--command` | Job command (e.g. `"sh /path/script.sh"`) |

## Architecture

```
mf_cli/
├── cli.py        # CLI entry point (argparse, service + job subcommands)
├── auth.py       # Playwright login, token caching
├── service.py    # Inference service CRUD (pure HTTP)
├── job.py        # Job CRUD (pure HTTP)
├── config.py     # API endpoints, GPU specs, defaults
└── pyproject.toml
```

- **Login**: Playwright fills the web login form → captures JWT cookie → caches to `~/.mf_cli/token` (6h TTL)
- **API**: All CRUD operations use `aimaster-token-header` JWT header, no browser needed after login
- **Billing**: GPU selection auto-generates a price token via `/apis/billing/.../sources-price`
