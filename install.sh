#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
TARGET_DIR="$(dirname "$SCRIPT_DIR")/mini-llm"

# rename extracted dir to mini-llm if needed (github tarballs use repo-branch)
if [ "$(basename "$SCRIPT_DIR")" = "mini-llm-main" ] && [ ! -d "$TARGET_DIR" ]; then
    mv "$SCRIPT_DIR" "$TARGET_DIR"
    cd "$TARGET_DIR"
else
    cd "$SCRIPT_DIR"
fi

# bootstrap uv if missing
if ! command -v uv &>/dev/null; then
    echo "  bootstrapping uv..."
    curl -LsSf https://astral.sh/uv/install.sh | sh
    export PATH="$HOME/.local/bin:$PATH"
fi

# run setup (--with installs deps, --no-project skips building a package)
uv run --with pyyaml --no-project python -m setup_mini "$@"
