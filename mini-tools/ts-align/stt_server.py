#!/usr/bin/env python3
"""Persistent STT server — keeps Whisper model loaded in memory.

Usage:
    uv run python stt_server.py [--port 8090] [--model mlx-community/whisper-large-v3-turbo]

Endpoints:
    POST /transcribe  — upload audio file, get text back
    GET  /health      — check if server + model are ready
"""
import argparse
import tempfile
import time
from http.server import HTTPServer, BaseHTTPRequestHandler
from pathlib import Path

import mlx_whisper

MODEL = None
MODEL_NAME = None


def load_model(model_name: str):
    global MODEL, MODEL_NAME
    MODEL_NAME = model_name
    print(f"  loading {model_name}...", flush=True)
    t0 = time.time()
    # Warm up by transcribing silence — forces model load
    mlx_whisper.transcribe(
        str(Path(__file__).parent / "silence.wav"),
        path_or_hf_repo=model_name,
        language="en",
    )
    MODEL = True
    print(f"  model ready ({time.time() - t0:.1f}s)", flush=True)


class Handler(BaseHTTPRequestHandler):
    def do_GET(self):
        if self.path == "/health":
            status = "ready" if MODEL else "loading"
            self.send_response(200)
            self.send_header("Content-Type", "text/plain")
            self.end_headers()
            self.wfile.write(f"{status}\n".encode())
        else:
            self.send_error(404)

    def do_POST(self):
        if self.path != "/transcribe":
            self.send_error(404)
            return

        if not MODEL:
            self.send_error(503, "Model still loading")
            return

        content_length = int(self.headers.get("Content-Length", 0))
        if content_length == 0:
            self.send_error(400, "No audio data")
            return

        # Read audio to temp file
        lang = self.headers.get("X-Language", "en")
        audio_data = self.rfile.read(content_length)

        with tempfile.NamedTemporaryFile(suffix=".wav", delete=False) as tmp:
            tmp.write(audio_data)
            tmp_path = tmp.name

        try:
            t0 = time.time()
            result = mlx_whisper.transcribe(
                tmp_path,
                path_or_hf_repo=MODEL_NAME,
                language=lang,
            )
            text = result.get("text", "").strip()
            elapsed = time.time() - t0
            print(f"  transcribed ({elapsed:.1f}s): {text[:60]}", flush=True)

            self.send_response(200)
            self.send_header("Content-Type", "text/plain")
            self.end_headers()
            self.wfile.write(f"{text}\n".encode())
        except Exception as e:
            self.send_error(500, str(e))
        finally:
            Path(tmp_path).unlink(missing_ok=True)

    def log_message(self, format, *args):
        pass  # quiet


def main():
    parser = argparse.ArgumentParser(description="STT server")
    parser.add_argument("--port", type=int, default=8090)
    parser.add_argument("--model", default="mlx-community/whisper-large-v3-turbo")
    args = parser.parse_args()

    load_model(args.model)

    server = HTTPServer(("0.0.0.0", args.port), Handler)
    print(f"  stt server listening on :{args.port}", flush=True)
    try:
        server.serve_forever()
    except KeyboardInterrupt:
        print("\n  shutting down")
        server.shutdown()


if __name__ == "__main__":
    main()
