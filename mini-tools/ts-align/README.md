# ts-align — Audio-Text Alignment

Generates word-level timestamps from audio using MLX Whisper. Runs locally on Apple Silicon, no API keys needed. Model downloads automatically on first use.

## Requirements

Only `uv`. Everything else is handled automatically.

## Usage

From anywhere in the repo:

```bash
# Align audio → JSON with word timestamps
uv run --project mini-tools/ts-align tsalign audio.mp3 -o sync.json

# Generate SRT subtitles
uv run --project mini-tools/ts-align tsalign audio.mp3 -o subtitles.srt

# Generate WebVTT
uv run --project mini-tools/ts-align tsalign audio.mp3 -o subtitles.vtt

# Use a smaller/faster model
uv run --project mini-tools/ts-align tsalign audio.mp3 -o sync.json -m mlx-community/whisper-small
```

## Pipeline with TTS

```bash
# Step 1: text to speech
uv run --project mini-tools/tts llmtts input.txt -o output.mp3

# Step 2: align for synchronized text
uv run --project mini-tools/ts-align tsalign output.mp3 -o output.srt
```

## Output formats

**JSON** (`.json`) — word-level timestamps:
```json
{
  "text": "The quick brown fox",
  "words": [
    {"word": "The", "start": 0.0, "end": 0.12},
    {"word": "quick", "start": 0.12, "end": 0.38}
  ],
  "segments": [...]
}
```

**SRT** (`.srt`) — standard subtitles, segment-level.

**VTT** (`.vtt`) — web subtitles, segment-level.

## Arguments

| Arg | Description | Default |
|-----|-------------|---------|
| `audio` | Input audio file | — |
| `-o FILE` | Output file (.json, .srt, .vtt) | `audio.json` |
| `-m MODEL` | Whisper model | `mlx-community/whisper-large-v3-turbo` |
| `-l LANG` | Language code | `en` |

## First run

First invocation downloads the Whisper model (~800 MB for large-v3-turbo). Subsequent runs are instant. Use `-m mlx-community/whisper-small` (~150 MB) for faster downloads and inference at slightly lower accuracy.
