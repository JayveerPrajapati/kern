from setuptools import setup, find_packages

setup(
    name="kern-sdk",
    version="0.1.0",
    description="Local-engine SDK for kern; the legacy kern-server REST client is deprecated",
    packages=find_packages(),
    python_requires=">=3.8",
)