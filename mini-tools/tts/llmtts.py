#!/usr/bin/env python3
"""llmtts - Text-to-speech using kokoro-tts"""
import itertools
import re
import sys
import tempfile
import threading
import time
import urllib.request
from pathlib import Path

from kokoro_tts import convert_text_to_audio


def number_to_words(n: int) -> str:
    """Convert integer to words."""
    if n == 0:
        return "zero"

    ones = ["", "one", "two", "three", "four", "five", "six", "seven", "eight", "nine",
            "ten", "eleven", "twelve", "thirteen", "fourteen", "fifteen", "sixteen",
            "seventeen", "eighteen", "nineteen"]
    tens = ["", "", "twenty", "thirty", "forty", "fifty", "sixty", "seventy", "eighty", "ninety"]

    def _convert(num):
        if num < 20:
            return ones[num]
        elif num < 100:
            return tens[num // 10] + (" " + ones[num % 10] if num % 10 else "")
        elif num < 1000:
            return ones[num // 100] + " hundred" + (" " + _convert(num % 100) if num % 100 else "")
        elif num < 1_000_000:
            return _convert(num // 1000) + " thousand" + (" " + _convert(num % 1000) if num % 1000 else "")
        elif num < 1_000_000_000:
            return _convert(num // 1_000_000) + " million" + (" " + _convert(num % 1_000_000) if num % 1_000_000 else "")
        else:
            return _convert(num // 1_000_000_000) + " billion" + (" " + _convert(num % 1_000_000_000) if num % 1_000_000_000 else "")

    return _convert(n)


def preprocess_for_tts(text: str) -> str:
    """Preprocess text to be more TTS-friendly."""
    def hex_to_speech(match):
        hex_val = match.group(1)
        spelled = " ".join(c.upper() if c.isalpha() else c for c in hex_val)
        return f"hash {spelled}"

    text = re.sub(r'0x([0-9a-fA-F]+)', hex_to_speech, text)

    def num_with_commas_to_words(match):
        num_str = match.group(0).replace(",", "")
        try:
            num = int(num_str)
            return number_to_words(num)
        except ValueError:
            return match.group(0)

    text = re.sub(r'\d{1,3}(?:,\d{3})+', num_with_commas_to_words, text)

    def large_num_to_words(match):
        try:
            num = int(match.group(0))
            if num >= 1000:
                return number_to_words(num)
            return match.group(0)
        except ValueError:
            return match.group(0)

    text = re.sub(r'\b\d{4,}\b', large_num_to_words, text)

    return text

# Pulse star animation (200ms per frame)
PULSE_FRAMES = ["✱", "✦", "·", "✦"]
ACTIVE_COLOR = "\033[38;5;214m"  # Yellow-orange
RESET = "\033[0m"

# Model files
MODEL_DIR = Path.home() / ".cache" / "llmtts"
MODEL_URL = "https://github.com/nazdridoy/kokoro-tts/releases/download/v1.0.0/kokoro-v1.0.onnx"
VOICES_URL = "https://github.com/nazdridoy/kokoro-tts/releases/download/v1.0.0/voices-v1.0.bin"
MODEL_FILE = MODEL_DIR / "kokoro-v1.0.onnx"
VOICES_FILE = MODEL_DIR / "voices-v1.0.bin"


def pulse_spinner(message: str, done: threading.Event):
    """Animated pulse spinner."""
    frames = itertools.cycle(PULSE_FRAMES)
    while not done.is_set():
        frame = next(frames)
        print(f"\r{ACTIVE_COLOR}{frame}{RESET} {message}", end="", file=sys.stderr, flush=True)
        time.sleep(0.2)
    print(f"\r  {message}", file=sys.stderr)


def download_file(url: str, dest: Path, desc: str):
    """Download a file with progress."""
    done = threading.Event()
    spinner_thread = threading.Thread(target=pulse_spinner, args=(f"Downloading {desc}...", done))
    spinner_thread.start()
    try:
        urllib.request.urlretrieve(url, dest)
    finally:
        done.set()
        spinner_thread.join()


def ensure_models():
    """Download model files if missing."""
    MODEL_DIR.mkdir(parents=True, exist_ok=True)

    if not MODEL_FILE.exists():
        download_file(MODEL_URL, MODEL_FILE, "model (kokoro-v1.0.onnx)")

    if not VOICES_FILE.exists():
        download_file(VOICES_URL, VOICES_FILE, "voices (voices-v1.0.bin)")


def clear_cache():
    """Remove downloaded model files."""
    import shutil
    if MODEL_DIR.exists():
        shutil.rmtree(MODEL_DIR)
        print(f"Cleared cache: {MODEL_DIR}", file=sys.stderr)
    else:
        print("Cache already empty", file=sys.stderr)


def tts(input_file: Path, output_file: Path | None = None, voice: str = "af_heart", speed: float = 1.0, stream: bool = False, pause: bool = False, readable: bool = False):
    ensure_models()

    if output_file is None:
        output_file = input_file.with_suffix(".mp3")

    fmt = output_file.suffix.lstrip(".") or "mp3"

    text = input_file.read_text()
    if readable:
        text = preprocess_for_tts(text)
    if pause:
        text = "... " + text
    word_count = len(text.split())

    # If text was modified, write to temp file
    if pause or readable:
        tmp = tempfile.NamedTemporaryFile(mode="w", suffix=".txt", delete=False)
        tmp.write(text)
        tmp.close()
        actual_input = Path(tmp.name)
    else:
        actual_input = input_file

    try:
        if stream:
            print(f"Playing {word_count} words...", file=sys.stderr)
            convert_text_to_audio(
                input_file=str(actual_input),
                output_file=str(output_file),
                voice=voice,
                speed=speed,
                stream=True,
                format=fmt,
                model_path=str(MODEL_FILE),
                voices_path=str(VOICES_FILE),
            )
            print(f"Saved to {output_file}", file=sys.stderr)
        else:
            message = f"Converting {word_count} words..."
            done = threading.Event()
            spinner_thread = threading.Thread(target=pulse_spinner, args=(message, done))
            spinner_thread.start()

            try:
                convert_text_to_audio(
                    input_file=str(actual_input),
                    output_file=str(output_file),
                    voice=voice,
                    speed=speed,
                    stream=False,
                    format=fmt,
                    model_path=str(MODEL_FILE),
                    voices_path=str(VOICES_FILE),
                )
            finally:
                done.set()
                spinner_thread.join()

            print(f"Saved to {output_file}", file=sys.stderr)
    finally:
        if pause or readable:
            actual_input.unlink()


def main():
    import argparse
    parser = argparse.ArgumentParser(description="llmtts - Text-to-speech")
    parser.add_argument("input", type=Path, nargs="?", help="Input text file")
    parser.add_argument("-t", "--text", help="Text string to speak")
    parser.add_argument("-o", "--output", type=Path, help="Output file (default: input.mp3)")
    parser.add_argument("-v", "--voice", default="af_heart")
    parser.add_argument("-s", "--speed", type=float, default=1.0)
    parser.add_argument("--stream", action="store_true", help="Also play audio to speakers")
    parser.add_argument("--pause", action="store_true", help="Add brief pause at start")
    parser.add_argument("--readable", action="store_true", help="Convert numbers and hex to spoken words")
    parser.add_argument("--clear-cache", action="store_true", help="Remove downloaded models")
    args = parser.parse_args()

    if args.clear_cache:
        clear_cache()
        return

    if args.text:
        with tempfile.NamedTemporaryFile(mode="w", suffix=".txt", delete=False) as tmp:
            tmp.write(args.text)
            input_file = Path(tmp.name)
        output_file = args.output or Path("output.mp3")
        tts(input_file, output_file=output_file, voice=args.voice, speed=args.speed, stream=args.stream, pause=args.pause, readable=args.readable)
        input_file.unlink()
    elif args.input:
        tts(args.input, output_file=args.output, voice=args.voice, speed=args.speed, stream=args.stream, pause=args.pause, readable=args.readable)
    else:
        parser.error("Either input file or --text is required")


if __name__ == "__main__":
    main()
