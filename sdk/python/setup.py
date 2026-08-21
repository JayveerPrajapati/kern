from setuptools import setup, find_packages

setup(
    name="kern-sdk",
    version="0.1.0",
    description="Thin HTTP client for the kern-server REST API",
    packages=find_packages(),
    python_requires=">=3.8",
)