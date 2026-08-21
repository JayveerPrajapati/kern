"""kern SDK — thin HTTP client for the kern-server REST API."""
from .client import Client, KernError

__all__ = ["Client", "KernError"]
__version__ = "0.1.0"