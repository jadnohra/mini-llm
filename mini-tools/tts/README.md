# tts — Text-to-Speech

Converts text to speech using kokoro-tts. Runs locally, no API keys needed. Models are downloaded automatically on first use (~80 MB to `~/.cache/llmtts/`).

## Requirements

Only `uv`. Everything else is handled automatically.

## Usage

From anywhere in the repo:

```bash
# Convert a text file to mp3
uv run --project mini-tools/tts llmtts input.txt -o output.mp3

# Convert inline text
uv run --project mini-tools/tts llmtts -t "Hello world" -o hello.mp3

# Stream to speakers while saving
uv run --project mini-tools/tts llmtts input.txt -o output.mp3 --stream

# Convert code-heavy text (numbers, hex become spoken words)
uv run --project mini-tools/tts llmtts input.txt -o output.mp3 --readable
```

## Arguments

| Arg | Description | Default |
|-----|-------------|---------|
| `input` | Input text file | — |
| `-t TEXT` | Inline text (instead of file) | — |
| `-o FILE` | Output file | `input.mp3` |
| `-v VOICE` | Voice name | `af_heart` |
| `-s SPEED` | Speed multiplier | `1.0` |
| `--stream` | Play audio to speakers while converting | off |
| `--readable` | Preprocess numbers/hex for speech | off |
| `--pause` | Add brief pause at start | off |
| `--clear-cache` | Remove downloaded models | — |

## Output

- Writes audio file to the path given by `-o`
- Supports mp3 and wav (determined by output file extension)
- Progress and status go to stderr; the file path is the only meaningful output

## First run

First invocation downloads two model files (~80 MB total). Subsequent runs are instant.
