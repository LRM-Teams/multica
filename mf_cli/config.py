"""Modelfactory CLI configuration and constants."""

from pathlib import Path

BASE_URL = "https://modelfactory.lenovo.com"
LOGIN_URL = f"{BASE_URL}/home"
SERVICE_URL = f"{BASE_URL}/home/service"
SERVICE_DETAIL_URL = f"{BASE_URL}/home/services_detail/largeService"

# API endpoints
API_AUTH_LOGIN = "/apis/auth/v1alpha1/login/v1alpha1"
API_INFERENCE_LARGE = "/apis/inference-large/v1alpha1/inference/v1alpha1"
API_BILLING_PRICE = "/apis/billing/v1alpha1/billing/v1alpha1/sources-price"
API_CONFIG = "/apis/inference-large/v1alpha1/configs/v1alpha1/configs/task-config"
API_STORAGE_DIRS = "/apis/storage/v1alpha1/core/v1alpha1/path/dirs"
API_STORAGE_TREE = "/apis/storage/v1alpha1/core/v1alpha1/path/trees"

# GPU spec options (from the form dropdown)
GPU_SPECS = [
    "A100-8", "A100-8-hg680x", "A100-8-srv",
    "A800-8", "A800-8-Premium", "A800-x",
    "H20-8", "H20-8-Premium",
    "P100-2", "RTX3090-8",
]

# Engine options
ENGINES = [
    "vllm-openai v0.24.0", "vllm-openai v0.23.0", "vllm-openai v0.22.1",
    "vllm-openai v0.21.0", "vllm-openai v0.19.1", "vllm-openai v0.18.0",
    "vllm-openai v0.17.1", "vllm-openai v0.12.0", "vllm-openai v0.10.2",
    "vllm-openai gemma4",
]

COOKIE_NAME = "aimaster-token-header"

# Default resources per GPU type (from config API)
DEFAULT_RESOURCES = {
    "a800": {"cpu": 8, "diskInGi": 8, "memoryInMi": 81920, "nvidiaGpu": 1},
    "h20":  {"cpu": 16, "diskInGi": 8, "memoryInMi": 81920, "nvidiaGpu": 1},
    "v100": {"cpu": 8, "diskInGi": 2, "memoryInMi": 81920, "nvidiaGpu": 1},
    "a100": {"cpu": 8, "diskInGi": 8, "memoryInMi": 81920, "nvidiaGpu": 1},
    "p100": {"cpu": 8, "diskInGi": 2, "memoryInMi": 81920, "nvidiaGpu": 1},
}

# Cache directory
# Job API
API_JOB_LIST = "/apis/job/v1alpha1/core/v1alpha1"
API_JOB_CREATE = API_JOB_LIST  # PUT to same URL creates a job

# Workspace image (from existing jobs)
DEFAULT_WORKSPACE_IMAGE = (
    "registry.docker.aimaster.lenovo.com:20443/library/765/workspace"
    ":app-workspace-765-1779089167283"
)

CACHE_DIR = Path.home() / ".mf_cli"
TOKEN_FILE = CACHE_DIR / "token"
SESSION_FILE = CACHE_DIR / "session.json"
