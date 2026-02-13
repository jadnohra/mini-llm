#!/usr/bin/env bash

cd "$(dirname "$0")"

# guard against running from inside a nested mini-llm-main
case "$PWD" in
    */mini-llm-main/*/mini-llm-main)
        echo "  ERROR: nested mini-llm-main detected — delete the outer one first."
        exit 1
        ;;
esac

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
