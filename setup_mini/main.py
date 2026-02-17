"""
mini-llm setup — one command to configure a headless Mac Mini AI server.

Usage:
    uv run python -m setup_mini              # check + install missing
    uv run python -m setup_mini --check      # only check, don't install
    uv run python -m setup_mini --test       # smoke-test installed services
    uv run python -m setup_mini --phase X    # run single phase
"""

import argparse
import os
import shutil
import subprocess
import sys
import time
from pathlib import Path

import yaml

# Ensure brew is on PATH — uv may not inherit the user's shell profile
for _brew_dir in ["/opt/homebrew/bin", "/usr/local/bin"]:
    if os.path.exists(os.path.join(_brew_dir, "brew")) and _brew_dir not in os.environ.get("PATH", ""):
        os.environ["PATH"] = _brew_dir + ":" + os.environ["PATH"]
        break

# ── Sound ───────────────────────────────────────────────

_sound = None


def beep():
    """Play alert chime via NSSound (non-blocking)."""
    global _sound
    try:
        from AppKit import NSSound
    except ImportError:
        subprocess.run(
            [sys.executable, "-m", "pip", "install", "pyobjc-framework-Cocoa", "-q"],
            capture_output=True,
        )
        try:
            from AppKit import NSSound
        except ImportError:
            return  # give up silently
    sound_path = str(Path(__file__).parent / "sounds" / "alert.wav")
    if _sound is None:
        _sound = NSSound.alloc().initWithContentsOfFile_byReference_(sound_path, True)
    if _sound:
        _sound.stop()
        _sound.play()


ENVS_DIR = Path.home() / ".mini" / "envs"



def env_python(name: str) -> str:
    """Return the python path for a local venv."""
    return str(ENVS_DIR / name / "bin" / "python")


def env_bin(name: str, cmd: str) -> str:
    """Return a binary path inside a local venv."""
    return str(ENVS_DIR / name / "bin" / cmd)


def ensure_env(name: str):
    """Create a venv if it doesn't exist."""
    venv_dir = ENVS_DIR / name
    if not venv_dir.exists():
        ENVS_DIR.mkdir(parents=True, exist_ok=True)
        run(f"uv venv --python 3.12 {venv_dir}", capture=False, check=False)


# ── Helpers ─────────────────────────────────────────────


def run(cmd: str, capture: bool = True, check: bool = True) -> subprocess.CompletedProcess:
    return subprocess.run(
        cmd, shell=True, capture_output=capture, text=True, check=check,
    )


def run_ok(cmd: str) -> bool:
    try:
        run(cmd, check=True)
        return True
    except (subprocess.CalledProcessError, FileNotFoundError):
        return False


def has_cmd(name: str) -> bool:
    return shutil.which(name) is not None


def brew_has(pkg: str) -> bool:
    return run_ok(f"brew list {pkg} 2>/dev/null")


def launchctl_load(label: str, plist_path):
    """Load a launchd plist using modern bootstrap (falls back to legacy load)."""
    uid = os.getuid()
    run(f"launchctl bootout gui/{uid}/{label} 2>/dev/null", check=False)
    r = run(f"launchctl bootstrap gui/{uid} {plist_path} 2>/dev/null", check=False)
    if r.returncode != 0:
        run(f"launchctl load {plist_path}", check=False)


def print_status(ok: bool, msg: str):
    mark = "\033[32mok\033[0m" if ok else "\033[31m x\033[0m"
    print(f"    {mark}  {msg}")


def print_header(name: str, missing: int):
    if missing == 0:
        status = "\033[32mALL OK\033[0m"
    else:
        status = f"\033[33m{missing} missing\033[0m"
    print(f"\n  \033[1m{name}\033[0m  {status}")


# ── Phase: system ───────────────────────────────────────


def check_system(cfg: dict) -> list[dict]:
    items = []

    # xcode cli tools
    ok = run_ok("xcode-select -p 2>/dev/null")
    items.append({"name": "Xcode CLI tools", "ok": ok, "action": "xcode"})

    # homebrew
    ok = has_cmd("brew")
    items.append({"name": "Homebrew", "ok": ok, "action": "brew"})

    # brew packages
    for pkg in cfg.get("system", {}).get("brew_packages", []):
        ok = brew_has(pkg)
        items.append({"name": f"brew: {pkg}", "ok": ok, "action": "brew_pkg", "pkg": pkg})

    return items


def apply_system(items: list[dict]):
    for item in items:
        if item["ok"]:
            continue
        action = item["action"]
        if action == "xcode":
            print(f"    installing {item['name']} (headless via softwareupdate)...")
            # create trigger file so softwareupdate finds CLT
            run("touch /tmp/.com.apple.dt.CommandLineTools.installondemand.in-progress", check=False)
            # find the CLT package name
            r = run("softwareupdate -l 2>/dev/null | grep -o '.*Command Line Tools.*' | head -1", check=False)
            pkg = r.stdout.strip().lstrip("* ").strip() if r.stdout else ""
            if pkg:
                print(f"    found: {pkg}")
                print(f"    installing... (this takes a few minutes)")
                run(f'softwareupdate -i "{pkg}" --verbose', capture=False, check=False)
            else:
                print("    could not find CLT package via softwareupdate.")
                print("    try: xcode-select --install (requires GUI)")
            # cleanup trigger
            run("rm -f /tmp/.com.apple.dt.CommandLineTools.installondemand.in-progress", check=False)
        elif action == "brew":
            print(f"    installing {item['name']}...")
            run('NONINTERACTIVE=1 /bin/bash -c "$(curl -fsSL https://raw.githubusercontent.com/Homebrew/install/HEAD/install.sh)"',
                capture=False, check=False)
            # add brew to PATH for this session
            for brew_dir in ["/opt/homebrew/bin", "/usr/local/bin"]:
                brew_bin = os.path.join(brew_dir, "brew")
                if os.path.exists(brew_bin):
                    os.environ["PATH"] = brew_dir + ":" + os.environ.get("PATH", "")
                    print(f"    added {brew_dir} to PATH")
                    break
        elif action == "brew_pkg":
            print(f"    installing {item['name']}...")
            run(f"brew install {item['pkg']}", capture=False, check=False)


# ── Phase: ssh ──────────────────────────────────────────


def check_ssh(cfg: dict) -> list[dict]:
    items = []

    # remote login enabled — check if sshd is running (no sudo needed)
    ok = run_ok("launchctl list | grep -q com.openssh.sshd")
    if not ok:
        # fallback: check if port 22 is listening
        ok = run_ok("nc -z localhost 22 2>/dev/null")
    items.append({"name": "Remote Login (SSH)", "ok": ok, "action": "enable_ssh"})

    return items


def apply_ssh(items: list[dict]):
    for item in items:
        if item["ok"]:
            continue
        if item["action"] == "enable_ssh":
            print(f"    enabling {item['name']}...")
            run("sudo systemsetup -setremotelogin on", capture=False, check=False)
            run("sudo launchctl load -w /System/Library/LaunchDaemons/ssh.plist 2>/dev/null", check=False)


# ── Phase: headless ─────────────────────────────────────


def check_headless(cfg: dict) -> list[dict]:
    items = []
    hcfg = cfg.get("headless", {})

    if hcfg.get("disable_sleep", False):
        # pmset -g works without sudo for reading
        r = run("pmset -g | grep -E '^ sleep' || true", check=False)
        ok = "0" in r.stdout if r.stdout else False
        items.append({"name": "Sleep disabled", "ok": ok, "action": "disable_sleep"})

    if hcfg.get("auto_restart_on_power_loss", False):
        r = run("pmset -g | grep -i autorestart || true", check=False)
        ok = "1" in r.stdout if r.stdout else False
        items.append({"name": "Auto-restart on power loss", "ok": ok, "action": "auto_restart"})

    return items


def apply_headless(items: list[dict]):
    for item in items:
        if item["ok"]:
            continue
        if item["action"] == "disable_sleep":
            print(f"    configuring {item['name']}...")
            run("sudo pmset -a sleep 0 displaysleep 0 disksleep 0", check=False)
        elif item["action"] == "auto_restart":
            print(f"    configuring {item['name']}...")
            run("sudo pmset -a autorestart 1", check=False)


# ── Phase: ollama ───────────────────────────────────────


def check_ollama(cfg: dict) -> list[dict]:
    items = []
    ocfg = cfg.get("ollama", {})
    if not ocfg.get("install", False):
        return items

    # binary
    ok = has_cmd("ollama")
    items.append({"name": "Ollama binary", "ok": ok, "action": "install_ollama"})

    # running
    ok = run_ok("curl -sf http://localhost:11434/api/tags >/dev/null 2>&1")
    items.append({"name": "Ollama running", "ok": ok, "action": "start_ollama"})

    # models
    if has_cmd("ollama"):
        r = run("ollama list 2>/dev/null || true", check=False)
        pulled = r.stdout.lower() if r.stdout else ""
        for model in ocfg.get("models", []):
            # ollama list shows model names, check if our model is there
            # model names can have tags like :32b, normalize
            base = model.split(":")[0].lower()
            ok = base in pulled
            items.append({
                "name": f"model: {model}", "ok": ok,
                "action": "pull_model", "model": model,
            })

    return items


def apply_ollama(items: list[dict]):
    for item in items:
        if item["ok"]:
            continue
        if item["action"] == "install_ollama":
            print(f"    installing Ollama...")
            run("curl -fsSL https://ollama.com/install.sh | sh", capture=False, check=False)
        elif item["action"] == "start_ollama":
            print(f"    starting Ollama...")
            # set OLLAMA_HOST so it binds to all interfaces
            os.environ["OLLAMA_HOST"] = "0.0.0.0"
            run("launchctl setenv OLLAMA_HOST 0.0.0.0", check=False)
            # try brew services first, fall back to nohup
            r = run("brew services start ollama 2>/dev/null", check=False)
            if r.returncode != 0:
                run("nohup ollama serve > /tmp/ollama.log 2>&1 &", check=False)
            # wait for it
            print(f"    waiting for Ollama to start...")
            for _ in range(30):
                if run_ok("curl -sf http://localhost:11434/api/tags >/dev/null 2>&1"):
                    break
                time.sleep(1)
        elif item["action"] == "pull_model":
            model = item["model"]
            print(f"    pulling {model}... (this may take a while)")
            run(f"ollama pull {model}", capture=False, check=False)


# ── Phase: llamacpp ─────────────────────────────────────


def check_llamacpp(cfg: dict) -> list[dict]:
    items = []
    lcfg = cfg.get("llamacpp", {})
    if not lcfg.get("install", False):
        return items

    llama_dir = Path.home() / "llama.cpp"
    ok = llama_dir.exists() and (llama_dir / "build" / "bin" / "llama-server").exists()
    items.append({"name": "llama.cpp built", "ok": ok, "action": "build_llamacpp"})

    ok = has_cmd("llama-server") or (Path.home() / "bin" / "llama-server").exists()
    items.append({"name": "llama-server in PATH", "ok": ok, "action": "link_llamacpp"})

    return items


def apply_llamacpp(items: list[dict]):
    llama_dir = Path.home() / "llama.cpp"
    bin_dir = Path.home() / "bin"

    for item in items:
        if item["ok"]:
            continue
        if item["action"] == "build_llamacpp":
            print(f"    cloning + building llama.cpp (Metal)...")
            if not llama_dir.exists():
                run(f"git clone https://github.com/ggerganov/llama.cpp.git {llama_dir}",
                    capture=False, check=False)
            build_dir = llama_dir / "build"
            build_dir.mkdir(exist_ok=True)
            run(f"cmake -B {build_dir} -S {llama_dir} -DGGML_METAL=ON",
                capture=False, check=False)
            run(f"cmake --build {build_dir} --config Release -j $(sysctl -n hw.ncpu)",
                capture=False, check=False)
        elif item["action"] == "link_llamacpp":
            print(f"    linking llama.cpp binaries to ~/bin...")
            bin_dir.mkdir(exist_ok=True)
            build_bin = llama_dir / "build" / "bin"
            if build_bin.exists():
                for binary in ["llama-server", "llama-cli"]:
                    src = build_bin / binary
                    dst = bin_dir / binary
                    if src.exists() and not dst.exists():
                        dst.symlink_to(src)
                        print(f"      linked {binary}")


# ── Phase: mlx ──────────────────────────────────────────


def check_mlx(cfg: dict) -> list[dict]:
    items = []
    mcfg = cfg.get("mlx", {})
    if not mcfg.get("install", False):
        return items

    # mlx-lm in local venv
    mlx_python = env_python("mlx")
    ok = os.path.exists(mlx_python) and run_ok(f"uv pip show --python {mlx_python} mlx-lm 2>/dev/null")
    items.append({"name": "mlx-lm installed", "ok": ok, "action": "install_mlx"})

    # models
    for model in mcfg.get("models", []):
        # check if model dir exists in huggingface cache
        cache_name = model.replace("/", "--")
        hf_cache = Path.home() / ".cache" / "huggingface" / "hub" / f"models--{cache_name}"
        ok = hf_cache.exists()
        items.append({
            "name": f"mlx: {model}", "ok": ok,
            "action": "download_mlx", "model": model,
        })

    return items


def apply_mlx(items: list[dict]):
    for item in items:
        if item["ok"]:
            continue
        if item["action"] == "install_mlx":
            print(f"    creating mlx venv + installing mlx-lm...")
            ensure_env("mlx")
            run(f"uv pip install --python {env_python('mlx')} mlx-lm", capture=False, check=False)
        elif item["action"] == "download_mlx":
            model = item["model"]
            print(f"    downloading {model}...")
            run(f'{env_python("mlx")} -c "from huggingface_hub import snapshot_download; snapshot_download(\'{model}\')"',
                capture=False, check=False)


# ── Phase: webui ────────────────────────────────────────


def check_webui(cfg: dict) -> list[dict]:
    items = []
    wcfg = cfg.get("open_webui", {})
    if not wcfg.get("install", False):
        return items

    webui_python = env_python("webui")
    ok = os.path.exists(webui_python) and run_ok(f"uv pip show --python {webui_python} open-webui 2>/dev/null")
    items.append({"name": "Open WebUI installed", "ok": ok, "action": "install_webui"})

    # check if launchd daemon plist exists with correct config
    plist = Path("/Library/LaunchDaemons/com.mini.open-webui.plist")
    expected_bin = env_bin("webui", "open-webui")
    plist_text = plist.read_text() if plist.exists() else ""
    ok = expected_bin in plist_text and "WorkingDirectory" in plist_text
    items.append({"name": "Open WebUI auto-start", "ok": ok, "action": "plist_webui"})

    # check if service is actually running
    ok = run_ok("sudo launchctl list com.mini.open-webui 2>/dev/null")
    items.append({"name": "Open WebUI running", "ok": ok, "action": "start_webui"})

    return items


def apply_webui(items: list[dict]):
    for item in items:
        if item["ok"]:
            continue
        if item["action"] == "install_webui":
            print(f"    creating webui venv + installing Open WebUI... (this takes a few minutes)")
            ensure_env("webui")
            run(f"uv pip install --python {env_python('webui')} open-webui", capture=False, check=False)
        elif item["action"] == "plist_webui":
            import secrets
            print(f"    creating auto-start daemon (needs sudo)...")
            # remove stale LaunchAgent if present
            old_plist = Path.home() / "Library" / "LaunchAgents" / "com.mini.open-webui.plist"
            if old_plist.exists():
                run(f"launchctl unload {old_plist} 2>/dev/null", check=False)
                old_plist.unlink()
            plist_path = Path("/Library/LaunchDaemons/com.mini.open-webui.plist")
            port = 8080
            user = os.environ.get("USER", "root")
            home = str(Path.home())
            plist_content = f"""<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>com.mini.open-webui</string>
    <key>UserName</key>
    <string>{user}</string>
    <key>ProgramArguments</key>
    <array>
        <string>{env_bin('webui', 'open-webui')}</string>
        <string>serve</string>
        <string>--port</string>
        <string>{port}</string>
    </array>
    <key>WorkingDirectory</key>
    <string>{home}</string>
    <key>EnvironmentVariables</key>
    <dict>
        <key>HOME</key>
        <string>{home}</string>
        <key>WEBUI_SECRET_KEY</key>
        <string>{secrets.token_hex(32)}</string>
        <key>DATA_DIR</key>
        <string>{home}/.open-webui</string>
    </dict>
    <key>RunAtLoad</key>
    <true/>
    <key>KeepAlive</key>
    <true/>
    <key>StandardOutPath</key>
    <string>{home}/Library/Logs/open-webui.log</string>
    <key>StandardErrorPath</key>
    <string>{home}/Library/Logs/open-webui.err</string>
</dict>
</plist>"""
            import tempfile
            with tempfile.NamedTemporaryFile(mode="w", suffix=".plist", delete=False) as tmp:
                tmp.write(plist_content)
                tmp_path = tmp.name
            run(f"sudo cp {tmp_path} {plist_path} && sudo chmod 644 {plist_path}", check=False)
            os.unlink(tmp_path)
            run(f"sudo launchctl bootout system/com.mini.open-webui 2>/dev/null", check=False)
            run(f"sudo launchctl bootstrap system {plist_path}", capture=False, check=False)
        elif item["action"] == "start_webui":
            print(f"    starting Open WebUI service...")
            plist_path = Path("/Library/LaunchDaemons/com.mini.open-webui.plist")
            run(f"sudo launchctl bootstrap system {plist_path} 2>/dev/null", check=False)
            run(f"sudo launchctl kickstart system/com.mini.open-webui 2>/dev/null", check=False)


# ── Phase: stt_server ──────────────────────────────────


def check_stt_server(cfg: dict) -> list[dict]:
    items = []
    repo = Path.home() / "mini-llm"
    stt_script = repo / "mini-tools" / "ts-align" / "stt_server.py"
    if not stt_script.exists():
        return items

    # check venv + mlx-whisper installed
    stt_python = env_python("stt")
    ok = os.path.exists(stt_python) and run_ok(f"uv pip show --python {stt_python} mlx-whisper 2>/dev/null")
    items.append({"name": "STT server deps", "ok": ok, "action": "install_stt"})

    # check launchd plist
    plist = Path("/Library/LaunchDaemons/com.mini.stt-server.plist")
    ok = plist.exists() and "stt_server.py" in (plist.read_text() if plist.exists() else "")
    items.append({"name": "STT server auto-start", "ok": ok, "action": "plist_stt"})

    # check if running
    ok = run_ok("sudo launchctl list com.mini.stt-server 2>/dev/null")
    items.append({"name": "STT server running", "ok": ok, "action": "start_stt"})

    return items


def apply_stt_server(items: list[dict]):
    for item in items:
        if item["ok"]:
            continue
        if item["action"] == "install_stt":
            print(f"    creating stt venv + installing mlx-whisper...")
            ensure_env("stt")
            run(f"uv pip install --python {env_python('stt')} mlx-whisper numpy", capture=False, check=False)
        elif item["action"] == "plist_stt":
            print(f"    creating STT server daemon (needs sudo)...")
            plist_path = Path("/Library/LaunchDaemons/com.mini.stt-server.plist")
            user = os.environ.get("USER", "root")
            home = str(Path.home())
            repo = f"{home}/mini-llm"
            plist_content = f"""<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>com.mini.stt-server</string>
    <key>UserName</key>
    <string>{user}</string>
    <key>ProgramArguments</key>
    <array>
        <string>{env_python('stt')}</string>
        <string>{repo}/mini-tools/ts-align/stt_server.py</string>
        <string>--port</string>
        <string>8090</string>
        <string>--idle-timeout</string>
        <string>300</string>
    </array>
    <key>WorkingDirectory</key>
    <string>{repo}/mini-tools/ts-align</string>
    <key>EnvironmentVariables</key>
    <dict>
        <key>HOME</key>
        <string>{home}</string>
    </dict>
    <key>RunAtLoad</key>
    <true/>
    <key>KeepAlive</key>
    <true/>
    <key>StandardOutPath</key>
    <string>{home}/Library/Logs/stt-server.log</string>
    <key>StandardErrorPath</key>
    <string>{home}/Library/Logs/stt-server.err</string>
</dict>
</plist>"""
            import tempfile
            with tempfile.NamedTemporaryFile(mode="w", suffix=".plist", delete=False) as tmp:
                tmp.write(plist_content)
                tmp_path = tmp.name
            run(f"sudo cp {tmp_path} {plist_path} && sudo chmod 644 {plist_path}", check=False)
            os.unlink(tmp_path)
            run(f"sudo launchctl bootout system/com.mini.stt-server 2>/dev/null", check=False)
            run(f"sudo launchctl bootstrap system {plist_path}", capture=False, check=False)
        elif item["action"] == "start_stt":
            print(f"    starting STT server...")
            plist_path = Path("/Library/LaunchDaemons/com.mini.stt-server.plist")
            run(f"sudo launchctl bootstrap system {plist_path} 2>/dev/null", check=False)
            run(f"sudo launchctl kickstart system/com.mini.stt-server 2>/dev/null", check=False)


# ── Phase: tailscale ───────────────────────────────────


def check_tailscale(cfg: dict) -> list[dict]:
    items = []
    tcfg = cfg.get("tailscale", {})
    if not tcfg.get("install", False):
        return items

    ok = has_cmd("tailscale")
    items.append({"name": "Tailscale", "ok": ok, "action": "install_tailscale"})

    return items


def apply_tailscale(items: list[dict]):
    for item in items:
        if item["ok"]:
            continue
        if item["action"] == "install_tailscale":
            print(f"    installing Tailscale...")
            run("brew install --cask tailscale", capture=False, check=False)


# ── Phase registry ──────────────────────────────────────

PHASES = [
    ("system", check_system, apply_system),
    ("ssh", check_ssh, apply_ssh),
    ("headless", check_headless, apply_headless),
    ("ollama", check_ollama, apply_ollama),
    ("llamacpp", check_llamacpp, apply_llamacpp),
    ("mlx", check_mlx, apply_mlx),
    ("webui", check_webui, apply_webui),
    ("stt_server", check_stt_server, apply_stt_server),
    ("tailscale", check_tailscale, apply_tailscale),
]


# ── Main ────────────────────────────────────────────────


def load_config() -> dict:
    # look for config.yaml in the repo root (next to setup_mini/)
    here = Path(__file__).parent.parent / "config.yaml"
    if not here.exists():
        print(f"  config.yaml not found at {here}")
        sys.exit(1)
    with open(here) as f:
        return yaml.safe_load(f)


OPTIONS = [
    ("ollama",           "Ollama",      "LLM serving daemon",                True),
    ("llamacpp",         "llama.cpp",   "Metal-accelerated inference",       True),
    ("mlx",              "MLX",         "Apple Silicon inference",           True),
    ("open_webui",       "Open WebUI",  "browser chat interface",            True),
    ("tailscale",        "Tailscale",   "remote access outside LAN",         False),
    ("headless_sleep",   "No sleep",    "keep Mini awake for SSH",           True),
    ("headless_restart", "Auto-restart","boot after power loss",             False),
]

# system and ssh are always included — they're prerequisites


def ask_options() -> dict:
    """Checkbox TUI. Arrow keys to move, space to toggle, enter to confirm."""
    import tty
    import termios

    selected = {key: default for key, _, _, default in OPTIONS}
    cursor = 0

    def read_key():
        fd = sys.stdin.fileno()
        old = termios.tcgetattr(fd)
        try:
            tty.setraw(fd)
            ch = sys.stdin.read(1)
            if ch == "\x1b":
                ch2 = sys.stdin.read(1)
                if ch2 == "[":
                    ch3 = sys.stdin.read(1)
                    if ch3 == "A":
                        return "up"
                    elif ch3 == "B":
                        return "down"
            elif ch == " ":
                return "space"
            elif ch in ("\r", "\n"):
                return "enter"
            elif ch == "\x03":  # ctrl-c
                return "quit"
            elif ch == "q":
                return "quit"
            return None
        finally:
            termios.tcsetattr(fd, termios.TCSADRAIN, old)

    def render():
        # move cursor up to redraw (except first render)
        sys.stdout.write(f"\033[{len(OPTIONS) + 4}A")
        sys.stdout.write("\033[J")  # clear to end of screen
        draw()

    def draw():
        print()
        print("  \033[1mWhat to install:\033[0m  (space: toggle, enter: confirm)")
        print()
        for i, (key, name, desc, _) in enumerate(OPTIONS):
            check = "\033[32mx\033[0m" if selected[key] else " "
            arrow = "\033[1m>\033[0m" if i == cursor else " "
            name_styled = f"\033[1m{name}\033[0m" if i == cursor else name
            print(f"  {arrow} [{check}]  {name_styled:<16} {desc}")
        print()

    # initial draw
    draw()

    while True:
        key = read_key()
        if key == "up":
            cursor = (cursor - 1) % len(OPTIONS)
        elif key == "down":
            cursor = (cursor + 1) % len(OPTIONS)
        elif key == "space":
            opt_key = OPTIONS[cursor][0]
            selected[opt_key] = not selected[opt_key]
        elif key == "enter":
            break
        elif key == "quit":
            print("  aborted.")
            sys.exit(0)
        render()

    return selected


def apply_choices(cfg: dict, choices: dict) -> dict:
    """Override config.yaml with interactive choices."""
    cfg.setdefault("ollama", {})["install"] = choices.get("ollama", False)
    cfg.setdefault("llamacpp", {})["install"] = choices.get("llamacpp", False)
    cfg.setdefault("mlx", {})["install"] = choices.get("mlx", False)
    cfg.setdefault("open_webui", {})["install"] = choices.get("open_webui", False)
    cfg.setdefault("tailscale", {})["install"] = choices.get("tailscale", False)
    cfg.setdefault("headless", {})["disable_sleep"] = choices.get("headless_sleep", False)
    cfg.setdefault("headless", {})["auto_restart_on_power_loss"] = choices.get("headless_restart", False)
    return cfg


def smoke_test(cfg: dict):
    """Hit each installed service with a real request."""
    print()
    print("  \033[1msmoke test\033[0m")
    print("  " + "─" * 48)
    print()

    passed = 0
    failed = 0

    def testing(name: str):
        sys.stdout.write(f"    \033[90mtesting {name}...\033[0m\r")
        sys.stdout.flush()

    def report(name: str, ok: bool, detail: str = ""):
        nonlocal passed, failed
        if ok:
            passed += 1
        else:
            failed += 1
        mark = "\033[32m  ok\033[0m" if ok else "\033[31m   x\033[0m"
        suffix = f"  ({detail})" if detail else ""
        print(f"\033[2K  {mark}  {name}{suffix}")

    # Ollama — generate one token (may take a minute to load model)
    if cfg.get("ollama", {}).get("install", False):
        models = cfg["ollama"].get("models", [])
        model = models[0] if models else "phi4-mini"
        testing(f"Ollama generate — may take a minute loading {model}")
        r = run(
            f'curl -sf --max-time 120 http://localhost:11434/api/generate -d \'{{"model":"{model}","prompt":"hi","stream":false,"options":{{"num_predict":1}}}}\'',
            check=False,
        )
        ok = r.returncode == 0 and r.stdout and "response" in r.stdout
        report(f"Ollama generate ({model})", ok)

    # llama.cpp — binary runs
    if cfg.get("llamacpp", {}).get("install", False):
        testing("llama-server --help")
        ok = run_ok("llama-server --help >/dev/null 2>&1") or run_ok(
            f"{Path.home()}/bin/llama-server --help >/dev/null 2>&1"
        )
        report("llama-server --help", ok)

    # MLX — import mlx_lm
    if cfg.get("mlx", {}).get("install", False):
        testing("import mlx_lm")
        mlx_py = env_python("mlx")
        ok = os.path.exists(mlx_py) and run_ok(f'{mlx_py} -c "import mlx_lm" 2>/dev/null')
        report("import mlx_lm", ok)

    # Open WebUI — HTTP response (may need time to start)
    if cfg.get("open_webui", {}).get("install", False):
        port = cfg["open_webui"].get("port", 8080)
        testing(f"Open WebUI on :{port} — waiting up to 30s for startup")
        ok = False
        for _ in range(15):
            if run_ok(f"curl -sf --max-time 2 http://localhost:{port}/ >/dev/null 2>&1"):
                ok = True
                break
            time.sleep(2)
        report(f"Open WebUI on :{port}", ok)

    print()
    total = passed + failed
    if failed == 0:
        print(f"  \033[32m{passed}/{total} passed.\033[0m")
    else:
        print(f"  \033[33m{passed}/{total} passed, {failed} failed.\033[0m")
    print()


def main():
    parser = argparse.ArgumentParser(description="mini-llm setup")
    parser.add_argument("--check", action="store_true", help="only check, don't install")
    parser.add_argument("--test", action="store_true", help="smoke-test installed services")
    parser.add_argument("--phase", type=str, help="run single phase")
    parser.add_argument("--yes", "-y", action="store_true", help="skip chooser, use config.yaml defaults")
    args = parser.parse_args()

    import platform
    if platform.machine() != "arm64" or platform.system() != "Darwin":
        print("  \033[31mThis script is for Apple Silicon Macs only.\033[0m")
        sys.exit(1)

    cfg = load_config()

    if args.test:
        smoke_test(cfg)
        return

    print()
    print("  \033[1mmini-llm setup\033[0m")
    print("  " + "─" * 48)

    # interactive chooser (skip with --yes, --check, --phase, or no TTY)
    if not args.yes and not args.check and not args.phase and sys.stdin.isatty():
        choices = ask_options()
        cfg = apply_choices(cfg, choices)
        print()

    # filter phases if --phase specified
    phases = PHASES
    if args.phase:
        phases = [(n, c, a) for n, c, a in PHASES if n == args.phase]
        if not phases:
            valid = ", ".join(n for n, _, _ in PHASES)
            print(f"  unknown phase: {args.phase}")
            print(f"  valid phases: {valid}")
            sys.exit(1)

    # scan
    total_missing = 0
    phase_results = []

    for name, check_fn, apply_fn in phases:
        items = check_fn(cfg)
        missing = sum(1 for i in items if not i["ok"])
        total_missing += missing
        phase_results.append((name, items, apply_fn, missing))

        print_header(name, missing)
        for item in items:
            print_status(item["ok"], item["name"])

    print()
    print(f"  {'─' * 48}")

    if total_missing == 0:
        print("  \033[32meverything is set up.\033[0m")
        print()
        return

    print(f"  {total_missing} item(s) to install/configure.")

    if args.check:
        print("  run without --check to install.")
        print()
        return

    print()

    # apply
    for name, items, apply_fn, missing in phase_results:
        if missing == 0:
            continue
        print(f"  \033[1m{name}\033[0m")
        apply_fn(items)
        print()

    # re-scan
    print("  " + "─" * 48)
    print("  \033[1mverifying...\033[0m")
    print()

    still_missing = 0
    for name, check_fn, _ in phases:
        items = check_fn(cfg)
        missing = sum(1 for i in items if not i["ok"])
        still_missing += missing
        print_header(name, missing)
        for item in items:
            print_status(item["ok"], item["name"])

    print()
    if still_missing == 0:
        print("  \033[32mall done.\033[0m")
    else:
        print(f"  \033[33m{still_missing} item(s) still need attention.\033[0m")
        print("  run again to retry, or fix manually.")
    print()


if __name__ == "__main__":
    try:
        main()
    except Exception as e:
        print(f"\n  \033[31mcrashed: {e}\033[0m")
