"""Modelfactory CLI configuration and constants."""

from pathlib import Path

BASE_URL = "https://modelfactory.lenovo.com"
LOGIN_URL = f"{BASE_URL}/home"

# API endpoints
API_BILLING_PRICE = "/apis/billing/v1alpha1/billing/v1alpha1/sources-price"
API_INFERENCE_LARGE = "/apis/inference-large/v1alpha1/inference/v1alpha1"
API_JOB = "/apis/job/v1alpha1/core/v1alpha1"
API_JOB_LIST = API_JOB
API_JOB_CREATE = API_JOB
API_WORKSPACE = "/apis/workspace/v1alpha1/core/v1alpha1"

# GPU spec options
GPU_SPECS = [
    "A800-8",
    "A800-8-Premium",
    "A800-x",
    "A100-8",
    "A100-8-hg680x",
    "A100-8-srv",
    "H20-8",
    "H20-8-Premium",
    "P100-2",
    "RTX3090-8",
]

COOKIE_NAME = "aimaster-token-header"

# Default resources per GPU type
DEFAULT_RESOURCES = {
    "a800": {"cpu": 8, "diskInGi": 2, "memoryInMi": 81920, "nvidiaGpu": 1},
    "h20": {"cpu": 16, "diskInGi": 8, "memoryInMi": 81920, "nvidiaGpu": 1},
    "v100": {"cpu": 8, "diskInGi": 2, "memoryInMi": 81920, "nvidiaGpu": 1},
    "a100": {"cpu": 8, "diskInGi": 8, "memoryInMi": 81920, "nvidiaGpu": 1},
    "p100": {"cpu": 8, "diskInGi": 2, "memoryInMi": 81920, "nvidiaGpu": 1},
}

# Default workspace image (PyTorch 2.8.0)
DEFAULT_WORKSPACE_IMAGE = (
    "registry.docker.aimaster.lenovo.com:20443/library/pytorch/pytorch:2.8.0"
)

# Cache directory
CACHE_DIR = Path.home() / ".mf_cli"
TOKEN_FILE = CACHE_DIR / "token"
SESSION_FILE = CACHE_DIR / "session.json"
