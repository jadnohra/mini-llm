#!/usr/bin/env bash
set -euo pipefail

# Bootstrap: assumes only macOS defaults (bash, curl).
# Installs uv if needed, then runs the Python setup.

echo
echo "  mini-llm bootstrap"
echo "  ────────────────────"

# install uv if missing
if ! command -v uv &>/dev/null; then
    echo "  installing uv..."
    curl -LsSf https://astral.sh/uv/install.sh | sh
    export PATH="$HOME/.local/bin:$PATH"
fi

echo "  uv: $(uv --version)"
echo

# run the actual setup
cd "$(dirname "$0")"
uv run python -m setup_mini "$@"
