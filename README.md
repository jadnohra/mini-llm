# mini-llm

One command to set up a Mac Mini as a headless AI server. Installs and configures everything needed to run LLMs locally over SSH.

## What it installs

- **Xcode CLI tools** — build essentials (headless-safe, no GUI needed)
- **Homebrew** + core tools (cmake, git, wget, jq, htop, tmux)
- **Ollama** — LLM serving daemon, bound to all interfaces so other machines can reach it
- **llama.cpp** — built from source with Metal acceleration
- **MLX** — Apple's inference framework optimized for Apple Silicon
- **Open WebUI** — browser-based chat interface, auto-starts on boot
- **SSH hardening** — enables remote login
- **Headless config** — disables sleep, auto-restart on power loss

## Install

```
curl -sL https://github.com/jadnohra/mini-llm/archive/refs/heads/main.tar.gz | tar xz && cd mini-llm-main && ./install.sh
```

That's it. Works on a fresh Mac Mini with nothing installed — only needs bash and curl (which macOS has by default).

The script is **idempotent** — run it again anytime and it only installs what's missing.

## Check without installing

```
./install.sh --check
```

Shows what's installed and what's missing, without touching anything.

## Run a single phase

```
./install.sh --phase ollama
```

Valid phases: `system`, `ssh`, `headless`, `ollama`, `llamacpp`, `mlx`, `webui`, `tailscale`

## Configure

Edit `config.yaml` before running to choose which models to pull, toggle features on/off, etc.

## License

MIT
