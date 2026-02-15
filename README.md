# 🍎🧠 mini-llm

A single command that turns a Mac Mini into a headless AI server. It installs Ollama, llama.cpp, MLX, and the surrounding tools, configures the machine for headless operation, and pulls the models you choose. The script detects what is already present and installs only what is missing.

## Install

```
curl -sL https://github.com/jadnohra/mini-llm/archive/refs/heads/main.tar.gz | tar xz && cd mini-llm-main && ./install.sh
```

The machine needs nothing beyond bash and curl, both of which ship with macOS. The script bootstraps its own dependencies, starting with uv and continuing through Xcode CLI tools and Homebrew if they are absent.

An interactive chooser appears on first run. Arrow keys navigate, spacebar toggles, enter confirms.

```
  What to install:  (space: toggle, enter: confirm)

  > [x]  Ollama           LLM serving daemon
    [x]  llama.cpp        Metal-accelerated inference
    [x]  MLX              Apple Silicon inference
    [x]  Open WebUI       browser chat interface
    [ ]  Tailscale        remote access outside LAN
    [x]  No sleep         keep Mini awake for SSH
    [ ]  Auto-restart     boot after power loss
```

A chime plays when the script finishes, whether it succeeded, failed, or was interrupted.

## What it sets up

**Ollama** runs as a daemon bound to all network interfaces, so other machines on the network can reach it. The script pulls whichever models are listed in `config.yaml`.

**llama.cpp** is built from source with Metal acceleration enabled and linked into `~/bin`. This gives direct control over context size, quantization, sampling parameters, and everything else Ollama abstracts away.

**MLX** is Apple's inference framework, purpose-built for unified memory on Apple Silicon. It tends to run 10-30% faster than llama.cpp on the same hardware.

**Open WebUI** provides a browser-based chat interface. A launchd plist starts it automatically on boot.

**Headless configuration** disables sleep so the machine remains reachable over SSH. Auto-restart after power loss is available but off by default.

## Running again

The script is idempotent. Running it a second time scans everything and touches only what has changed or is newly missing.

```
cd mini-llm-main && ./install.sh
```

## Checking without installing

```
./install.sh --check
```

This scans and reports without modifying anything.

## Smoke-testing

```
./install.sh --test
```

This hits each running service with a real request: generates a token via Ollama, imports `mlx_lm`, checks the Open WebUI HTTP endpoint, and verifies `llama-server` runs.

## Running a single phase

```
./install.sh --phase ollama
```

Valid phases: `system`, `ssh`, `headless`, `ollama`, `llamacpp`, `mlx`, `webui`, `tailscale`

## Configuration

`config.yaml` lists which models to pull, which brew packages to install, and which features to enable. Edit it before running, or use the interactive chooser and leave the file as a reference.

## CLI

The `mini` command runs on your laptop and talks to the Mac Mini over SSH.

```
make build        # compile to bin/mini
make install      # copy to ~/bin
```

```
mini status       # services, memory, disk, load
mini models       # list pulled models
mini selftest     # 5-step connectivity smoke test
mini ask PROMPT   # single-shot prompt with streaming
```

`mini status --json` and `mini models --json` produce machine-readable output.

### mini ask

Send a prompt and stream the response:

```
mini ask "write a fibonacci function in Go"
mini ask -m deepseek-r1:32b "review this for race conditions"
mini ask -s "You are a Go expert" "refactor this"
mini ask --no-stream "short question"
```

| Flag | Description | Default |
|------|-------------|---------|
| `-m, --model` | model name | from config |
| `-s, --system` | system prompt | — |
| `-t, --temp` | temperature | `0.3` |
| `-n, --max-tokens` | max output tokens | `4096` |
| `--no-stream` | wait for full response | off |

### CLI configuration

The CLI reads from `config.yaml` (the `cli:` section) and `~/.mini/config.yaml`. It errors out if required fields are missing.

| Setting | Description |
|---------|-------------|
| `host` | hostname of the Mac Mini |
| `ssh_user` | SSH username |
| `ollama_port` | Ollama port |
| `llamacpp_port` | llama.cpp port |
| `default_model` | model for `mini ask` |

## Mini-tools

Standalone tools in `mini-tools/`. Each is self-contained with its own dependencies.

| Tool | Description |
|------|-------------|
| [tts](mini-tools/tts/README.md) | Text-to-speech using kokoro-tts |

## License

MIT
