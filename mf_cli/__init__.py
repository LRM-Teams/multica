"""mf_cli — Modelfactory CLI for managing inference services and jobs."""

from .service import create_service, list_services, get_service_status
from .job import create_job, list_jobs, get_job_status
from .auth import login, get_token

__version__ = "0.2.0"
__all__ = [
    "create_service", "list_services", "get_service_status",
    "create_job", "list_jobs", "get_job_status",
    "login", "get_token",
]
