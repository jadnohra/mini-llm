"""
mini-llm setup — one command to configure a headless Mac Mini AI server.

Usage:
    uv run python -m setup_mini              # check + install missing
    uv run python -m setup_mini --check      # only check, don't install
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
            run('/bin/bash -c "$(curl -fsSL https://raw.githubusercontent.com/Homebrew/install/HEAD/install.sh)"',
                capture=False, check=False)
            # add brew to path for this session
            brew_paths = ["/opt/homebrew/bin/brew", "/usr/local/bin/brew"]
            for bp in brew_paths:
                if os.path.exists(bp):
                    result = run(f"{bp} shellenv", check=False)
                    if result.returncode == 0:
                        for line in result.stdout.strip().split("\n"):
                            if line.startswith("export "):
                                parts = line[7:].split("=", 1)
                                if len(parts) == 2:
                                    key = parts[0]
                                    val = parts[1].strip('"').strip("'")
                                    os.environ[key] = val
                    break
        elif action == "brew_pkg":
            print(f"    installing {item['name']}...")
            run(f"brew install {item['pkg']}", capture=False, check=False)


# ── Phase: ssh ──────────────────────────────────────────


def check_ssh(cfg: dict) -> list[dict]:
    items = []

    # remote login enabled
    ok = run_ok("sudo systemsetup -getremotelogin 2>/dev/null | grep -qi 'on'")
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
        # check if sleep is disabled (sleep = 0 means never)
        r = run("sudo pmset -g | grep -E '^ sleep' || true", check=False)
        ok = "0" in r.stdout if r.stdout else False
        items.append({"name": "Sleep disabled", "ok": ok, "action": "disable_sleep"})

    if hcfg.get("auto_restart_on_power_loss", False):
        r = run("sudo pmset -g | grep -i autorestart || true", check=False)
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
            run("launchctl setenv OLLAMA_HOST 0.0.0.0", check=False)
            run("brew services start ollama 2>/dev/null || ollama serve &", check=False)
            # wait for it
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

    # mlx-lm package
    ok = run_ok("python3 -c 'import mlx_lm' 2>/dev/null")
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
            print(f"    installing mlx-lm...")
            run("pip3 install mlx-lm", capture=False, check=False)
        elif item["action"] == "download_mlx":
            model = item["model"]
            print(f"    downloading {model}...")
            run(f'python3 -c "from huggingface_hub import snapshot_download; snapshot_download(\'{model}\')"',
                capture=False, check=False)


# ── Phase: webui ────────────────────────────────────────


def check_webui(cfg: dict) -> list[dict]:
    items = []
    wcfg = cfg.get("open_webui", {})
    if not wcfg.get("install", False):
        return items

    ok = run_ok("pip3 show open-webui 2>/dev/null")
    items.append({"name": "Open WebUI installed", "ok": ok, "action": "install_webui"})

    # check if launchd plist exists
    plist = Path.home() / "Library" / "LaunchAgents" / "com.mini.open-webui.plist"
    ok = plist.exists()
    items.append({"name": "Open WebUI auto-start", "ok": ok, "action": "plist_webui"})

    return items


def apply_webui(items: list[dict]):
    for item in items:
        if item["ok"]:
            continue
        if item["action"] == "install_webui":
            print(f"    installing Open WebUI... (this takes a few minutes)")
            run("pip3 install open-webui", capture=False, check=False)
        elif item["action"] == "plist_webui":
            print(f"    creating auto-start plist...")
            plist_dir = Path.home() / "Library" / "LaunchAgents"
            plist_dir.mkdir(parents=True, exist_ok=True)
            plist_path = plist_dir / "com.mini.open-webui.plist"
            port = 8080
            plist_content = f"""<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>com.mini.open-webui</string>
    <key>ProgramArguments</key>
    <array>
        <string>/usr/bin/env</string>
        <string>open-webui</string>
        <string>serve</string>
        <string>--port</string>
        <string>{port}</string>
    </array>
    <key>RunAtLoad</key>
    <true/>
    <key>KeepAlive</key>
    <true/>
    <key>StandardOutPath</key>
    <string>{Path.home()}/Library/Logs/open-webui.log</string>
    <key>StandardErrorPath</key>
    <string>{Path.home()}/Library/Logs/open-webui.err</string>
</dict>
</plist>"""
            plist_path.write_text(plist_content)
            run(f"launchctl load {plist_path}", check=False)


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


def main():
    parser = argparse.ArgumentParser(description="mini-llm setup")
    parser.add_argument("--check", action="store_true", help="only check, don't install")
    parser.add_argument("--phase", type=str, help="run single phase")
    args = parser.parse_args()

    cfg = load_config()

    print()
    print("  \033[1mmini-llm setup\033[0m")
    print("  " + "─" * 48)

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

    # confirm
    print()
    try:
        answer = input("  proceed? [Y/n] ").strip().lower()
    except (EOFError, KeyboardInterrupt):
        print("\n  aborted.")
        return

    if answer and answer not in ("y", "yes"):
        print("  aborted.")
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
    main()
