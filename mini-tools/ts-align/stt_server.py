#!/usr/bin/env python3
"""Persistent STT server — keeps Whisper model loaded in memory.

Usage:
    uv run python stt_server.py [--port 8090] [--model mlx-community/whisper-large-v3-turbo]

Model is loaded lazily on first /transcribe call and unloaded after --idle-timeout
seconds of inactivity to free memory for other models (Ollama etc).

Endpoints:
    POST /transcribe  — upload audio file, get text back
    GET  /health      — check server status (idle / loaded)
"""
import argparse
import gc
import tempfile
import threading
import time
from http.server import HTTPServer, BaseHTTPRequestHandler
from pathlib import Path

import mlx.core
import mlx_whisper

_model_name = None
_model_loaded = False
_last_use = 0.0
_idle_timeout = 300
_unload_timer = None
# Guards model load/unload against concurrent transcription.
# The timer thread acquires this before unloading; the request handler
# holds it during transcription. HTTPServer is single-threaded so requests
# are serialized, but the timer thread runs separately.
_lock = threading.Lock()


def _load():
    global _model_loaded, _last_use
    if _model_loaded:
        return
    print(f"  loading {_model_name}...", flush=True)
    t0 = time.time()
    # Trigger model download/load by transcribing a tiny silent buffer.
    import numpy as np
    silence = np.zeros(1600, dtype=np.float32)
    mlx_whisper.transcribe(silence, path_or_hf_repo=_model_name, language="en")
    _model_loaded = True
    _last_use = time.time()
    print(f"  model ready ({time.time() - t0:.1f}s)", flush=True)


def _unload():
    """Called by timer thread after idle timeout."""
    global _model_loaded, _unload_timer
    with _lock:
        if not _model_loaded:
            return
        if time.time() - _last_use < _idle_timeout:
            # Got used recently — reschedule
            _schedule_unload_unlocked()
            return
        _model_loaded = False
        _unload_timer = None
    # Free memory outside the lock
    mlx.core.metal.clear_cache()
    gc.collect()
    print("  model unloaded (idle timeout)", flush=True)


def _schedule_unload_unlocked():
    """Schedule unload timer. Caller must hold _lock or be in single-threaded context."""
    global _unload_timer
    if _idle_timeout <= 0:
        return
    if _unload_timer is not None:
        _unload_timer.cancel()
    _unload_timer = threading.Timer(_idle_timeout, _unload)
    _unload_timer.daemon = True
    _unload_timer.start()


class Handler(BaseHTTPRequestHandler):
    def do_GET(self):
        if self.path == "/health":
            status = "loaded" if _model_loaded else "idle"
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

        content_length = int(self.headers.get("Content-Length", 0))
        if content_length == 0:
            self.send_error(400, "No audio data")
            return

        lang = self.headers.get("X-Language", "en")
        content_type = self.headers.get("Content-Type", "audio/wav")
        suffix = ".m4a" if "m4a" in content_type else ".wav"
        audio_data = self.rfile.read(content_length)

        with tempfile.NamedTemporaryFile(suffix=suffix, delete=False) as tmp:
            tmp.write(audio_data)
            tmp_path = tmp.name

        try:
            with _lock:
                global _last_use
                _load()
                _last_use = time.time()
                _schedule_unload_unlocked()
                t0 = time.time()
                result = mlx_whisper.transcribe(
                    tmp_path,
                    path_or_hf_repo=_model_name,
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
    parser.add_argument("--idle-timeout", type=int, default=300,
                        help="seconds of inactivity before unloading model (0=never)")
    args = parser.parse_args()

    global _model_name, _idle_timeout
    _model_name = args.model
    _idle_timeout = args.idle_timeout

    HTTPServer.allow_reuse_address = True
    server = HTTPServer(("0.0.0.0", args.port), Handler)
    print(f"  stt server listening on :{args.port} (lazy load, idle timeout {_idle_timeout}s)", flush=True)
    try:
        server.serve_forever()
    except KeyboardInterrupt:
        print("\n  shutting down")
        server.shutdown()


if __name__ == "__main__":
    main()
