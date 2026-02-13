#!/usr/bin/env bash
set -euo pipefail

cd "$(dirname "$0")"

# bootstrap uv if missing
if ! command -v uv &>/dev/null; then
    echo "  bootstrapping uv..."
    curl -LsSf https://astral.sh/uv/install.sh | sh
    export PATH="$HOME/.local/bin:$PATH"
fi

# run setup
uv run python -m setup_mini "$@"
