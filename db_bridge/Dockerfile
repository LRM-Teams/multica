FROM ghcr.io/astral-sh/uv:0.11.16-python3.14-trixie

WORKDIR /app

# Install Python dependencies
COPY pyproject.toml ./
ENV UV_LINK_MODE=copy
RUN --mount=type=cache,target=/root/.cache/uv uv pip install --system -e ".[crypto]" || uv pip install --system -e "."

# Copy application code
COPY . .

ENV PYTHONPATH=/app

# Default: run stub (override via docker-compose command)
CMD ["python", "-m", "db_bridge.run_stub", "--side", "leagent"]
