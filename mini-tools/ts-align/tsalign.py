#!/usr/bin/env python3
"""tsalign - Audio-text alignment using MLX Whisper"""
import itertools
import json
import sys
import threading
import time
from pathlib import Path

PULSE_FRAMES = ["✱", "✦", "·", "✦"]
ACTIVE_COLOR = "\033[38;5;214m"
RESET = "\033[0m"

DEFAULT_MODEL = "mlx-community/whisper-large-v3-turbo"


def pulse_spinner(message: str, done: threading.Event):
    frames = itertools.cycle(PULSE_FRAMES)
    while not done.is_set():
        frame = next(frames)
        print(f"\r{ACTIVE_COLOR}{frame}{RESET} {message}", end="", file=sys.stderr, flush=True)
        time.sleep(0.2)
    print(f"\r  {message}", file=sys.stderr)


def transcribe(audio_path: Path, model: str = DEFAULT_MODEL, language: str = "en") -> dict:
    """Transcribe audio with word-level timestamps using MLX Whisper."""
    import mlx_whisper

    result = mlx_whisper.transcribe(
        str(audio_path),
        path_or_hf_repo=model,
        word_timestamps=True,
        language=language,
    )
    return result


def extract_words(result: dict) -> list[dict]:
    """Extract flat word list from Whisper result."""
    words = []
    for seg in result.get("segments", []):
        for w in seg.get("words", []):
            words.append({
                "word": w["word"].strip(),
                "start": round(w["start"], 3),
                "end": round(w["end"], 3),
            })
    return words


def extract_segments(result: dict) -> list[dict]:
    """Extract segment list from Whisper result."""
    segments = []
    for seg in result.get("segments", []):
        segments.append({
            "text": seg["text"].strip(),
            "start": round(seg["start"], 3),
            "end": round(seg["end"], 3),
            "words": [
                {"word": w["word"].strip(), "start": round(w["start"], 3), "end": round(w["end"], 3)}
                for w in seg.get("words", [])
            ],
        })
    return segments


def fmt_ts_srt(seconds: float) -> str:
    """Format seconds as SRT timestamp: HH:MM:SS,mmm"""
    h = int(seconds // 3600)
    m = int((seconds % 3600) // 60)
    s = int(seconds % 60)
    ms = int((seconds % 1) * 1000)
    return f"{h:02d}:{m:02d}:{s:02d},{ms:03d}"


def fmt_ts_vtt(seconds: float) -> str:
    """Format seconds as VTT timestamp: HH:MM:SS.mmm"""
    h = int(seconds // 3600)
    m = int((seconds % 3600) // 60)
    s = int(seconds % 60)
    ms = int((seconds % 1) * 1000)
    return f"{h:02d}:{m:02d}:{s:02d}.{ms:03d}"


def to_json(result: dict) -> str:
    """Convert to JSON with words and segments."""
    return json.dumps({
        "text": result.get("text", "").strip(),
        "segments": extract_segments(result),
        "words": extract_words(result),
    }, indent=2)


def to_srt(result: dict) -> str:
    """Convert to SRT subtitle format."""
    lines = []
    for i, seg in enumerate(result.get("segments", []), 1):
        start = fmt_ts_srt(seg["start"])
        end = fmt_ts_srt(seg["end"])
        text = seg["text"].strip()
        lines.append(f"{i}\n{start} --> {end}\n{text}\n")
    return "\n".join(lines)


def to_vtt(result: dict) -> str:
    """Convert to WebVTT subtitle format."""
    lines = ["WEBVTT\n"]
    for seg in result.get("segments", []):
        start = fmt_ts_vtt(seg["start"])
        end = fmt_ts_vtt(seg["end"])
        text = seg["text"].strip()
        lines.append(f"{start} --> {end}\n{text}\n")
    return "\n".join(lines)


def align(audio_path: Path, output_path: Path, model: str = DEFAULT_MODEL, language: str = "en"):
    """Align audio and write output in the format determined by file extension."""
    done = threading.Event()
    spinner = threading.Thread(target=pulse_spinner, args=(f"Aligning {audio_path.name}...", done))
    spinner.start()

    try:
        result = transcribe(audio_path, model=model, language=language)
    finally:
        done.set()
        spinner.join()

    ext = output_path.suffix.lower()
    if ext == ".json":
        content = to_json(result)
    elif ext == ".srt":
        content = to_srt(result)
    elif ext == ".vtt":
        content = to_vtt(result)
    else:
        print(f"Unknown format {ext}, defaulting to JSON", file=sys.stderr)
        content = to_json(result)

    output_path.write_text(content)

    # Summary
    words = extract_words(result)
    duration = words[-1]["end"] if words else 0
    print(f"  {len(words)} words · {duration:.1f}s · {output_path}", file=sys.stderr)


def main():
    import argparse

    parser = argparse.ArgumentParser(description="tsalign - Audio-text alignment with word timestamps")
    parser.add_argument("audio", type=Path, help="Input audio file (mp3, wav, etc.)")
    parser.add_argument("-o", "--output", type=Path, help="Output file (.json, .srt, or .vtt)")
    parser.add_argument("-m", "--model", default=DEFAULT_MODEL, help=f"Whisper model (default: {DEFAULT_MODEL})")
    parser.add_argument("-l", "--language", default="en", help="Language code (default: en)")
    args = parser.parse_args()

    if not args.audio.exists():
        parser.error(f"File not found: {args.audio}")

    output = args.output or args.audio.with_suffix(".json")
    align(args.audio, output, model=args.model, language=args.language)


if __name__ == "__main__":
    main()
