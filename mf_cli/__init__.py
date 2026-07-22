"""mf_cli — Modelfactory CLI for managing services, jobs, and workspaces."""

from .service import create_service, list_services, get_service_status
from .job import create_job, list_jobs, get_job_status
from .workspace import (create_workspace, list_workspaces, restart_workspace,
                        stop_workspace, save_workspace, delete_workspace)
from .auth import login, get_token

__version__ = "0.3.0"
