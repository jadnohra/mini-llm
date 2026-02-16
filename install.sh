#!/usr/bin/env bash

# mini-llm installer
#
# Fresh install (curl pipe):
#   curl -sL https://raw.githubusercontent.com/jadnohra/mini-llm/main/install.sh | bash
#
# Re-run from cloned repo:
#   cd ~/mini-llm && ./install.sh

REPO_URL="https://github.com/jadnohra/mini-llm.git"
INSTALL_DIR="$HOME/mini-llm"

# ── Detect context ───────────────────────────────────────
# If setup_mini/ exists relative to this script, we're inside the repo.
# Otherwise we're in bootstrap mode (curl pipe or standalone run).

SCRIPT_DIR=""
if [ -n "${BASH_SOURCE[0]:-}" ] && [ -f "$(dirname "${BASH_SOURCE[0]}")/setup_mini/__main__.py" ]; then
    SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
fi

if [ -n "$SCRIPT_DIR" ]; then
    # ── Direct mode: already inside the repo ─────────────
    cd "$SCRIPT_DIR"
else
    # ── Bootstrap mode: ensure git, clone, cd ────────────

    # Stage 1: Ensure git (macOS ships a shim that triggers Xcode CLT install)
    if ! git --version &>/dev/null; then
        echo "  Xcode Command Line Tools needed (for git)..."
        xcode-select --install 2>/dev/null || true
        echo "  Waiting for installation — click Install in the dialog..."
        until xcode-select -p &>/dev/null; do sleep 5; done
        echo "  Xcode CLT installed."
    fi

    # Stage 2: Clone or update
    if [ -d "$INSTALL_DIR/.git" ]; then
        echo "  Updating $INSTALL_DIR..."
        git -C "$INSTALL_DIR" pull --ff-only
    else
        if [ -d "$INSTALL_DIR" ]; then
            echo "  $INSTALL_DIR exists but is not a git repo — backing up."
            mv "$INSTALL_DIR" "${INSTALL_DIR}.bak.$(date +%s)"
        fi
        echo "  Cloning mini-llm into $INSTALL_DIR..."
        git clone "$REPO_URL" "$INSTALL_DIR"
    fi

    cd "$INSTALL_DIR"
fi

# ── Common setup (both modes) ────────────────────────────

SOUND="setup_mini/sounds/alert.wav"

chime() {
    # try NSSound via python, fall back to afplay
    python3 -c "
from AppKit import NSSound
import time
s = NSSound.alloc().initWithContentsOfFile_byReference_('$SOUND', True)
s.play()
time.sleep(1)
" 2>/dev/null || afplay "$SOUND" 2>/dev/null || true
}

trap chime EXIT

set -euo pipefail

# bootstrap uv if missing
if ! command -v uv &>/dev/null; then
    echo "  bootstrapping uv..."
    curl -LsSf https://astral.sh/uv/install.sh | sh
    export PATH="$HOME/.local/bin:$PATH"
fi

# run setup (--with installs deps, --no-project skips building a package)
uv run --with pyyaml --no-project python -m setup_mini "$@"
