#!/usr/bin/env python3
"""LatentSync wrapper for MPS (Apple Silicon).

Setup: installs system deps (ffmpeg), clones LatentSync, creates venv,
       installs Python deps, patches for MPS, downloads v1.5 checkpoints.
Inference: runs patched inference.py on MPS GPU.
"""

import argparse
import json
import os
import shutil
import subprocess
import sys
import time

BASE_DIR = os.path.expanduser("~/.mini/lipsync")
REPO_DIR = os.path.join(BASE_DIR, "LatentSync")
VENV_DIR = os.path.join(BASE_DIR, "venv")
VENV_PYTHON = os.path.join(VENV_DIR, "bin", "python")
MARKER = os.path.join(BASE_DIR, ".setup-done")
BENCHMARK_CACHE = os.path.join(BASE_DIR, ".benchmark.json")
SCRIPT_DIR = os.path.dirname(os.path.abspath(__file__))
REQUIREMENTS = os.path.join(SCRIPT_DIR, "requirements-mps.txt")
PATCH_SCRIPT = os.path.join(SCRIPT_DIR, "patch_mps.py")

# System tools required for inference (installed via brew)
BREW_DEPS = ["ffmpeg"]


def ensure_brew_path():
    """Ensure Homebrew is on PATH (SSH sessions often miss it)."""
    for brew_dir in ["/opt/homebrew/bin", "/usr/local/bin"]:
        if os.path.exists(os.path.join(brew_dir, "brew")) and brew_dir not in os.environ.get("PATH", ""):
            os.environ["PATH"] = brew_dir + ":" + os.environ["PATH"]
            break


def ensure_system_deps():
    """Install system dependencies via brew."""
    ensure_brew_path()
    if not shutil.which("brew"):
        print("WARNING: Homebrew not found — cannot install system deps.")
        print("  Install manually: brew install " + " ".join(BREW_DEPS))
        return
    for dep in BREW_DEPS:
        if shutil.which(dep):
            print(f"  OK   {dep}")
            continue
        print(f"  Installing {dep}...")
        run(f"brew install {dep}")

# Critical imports that must work after install
VERIFY_IMPORTS = ["torch", "diffusers", "transformers", "decord", "cv2", "mediapipe"]


def run(cmd, **kwargs):
    print(f"  $ {cmd}")
    result = subprocess.run(cmd, shell=True, **kwargs)
    if result.returncode != 0:
        print(f"  FAILED (exit {result.returncode})")
        sys.exit(1)
    return result


def verify_install():
    """Verify critical packages are importable in the venv."""
    print("Verifying installation...")
    failed = []
    for mod in VERIFY_IMPORTS:
        result = subprocess.run(
            [VENV_PYTHON, "-c", f"import {mod}"],
            capture_output=True, text=True,
        )
        if result.returncode != 0:
            print(f"  FAIL {mod}")
            failed.append(mod)
        else:
            print(f"  OK   {mod}")
    if failed:
        print(f"\nMissing packages: {', '.join(failed)}")
        print("Attempting individual install...")
        for mod in failed:
            # Map module names to pip package names
            pip_name = {
                "decord": "eva-decord",
                "cv2": "opencv-python",
            }.get(mod, mod)
            run(f"uv pip install --python {VENV_PYTHON} {pip_name}")
        # Re-verify
        still_failed = []
        for mod in failed:
            result = subprocess.run(
                [VENV_PYTHON, "-c", f"import {mod}"],
                capture_output=True, text=True,
            )
            if result.returncode != 0:
                still_failed.append(mod)
        if still_failed:
            print(f"\nFATAL: Could not install: {', '.join(still_failed)}")
            sys.exit(1)
    print("All imports verified.")


def setup():
    """First-time setup: system deps, clone, venv, deps, patch, checkpoints."""
    os.makedirs(BASE_DIR, exist_ok=True)

    # System dependencies (brew)
    print("Checking system dependencies...")
    ensure_system_deps()

    # Clone LatentSync
    if not os.path.isdir(REPO_DIR):
        print("Cloning LatentSync...")
        run(f"git clone https://github.com/bytedance/LatentSync.git {REPO_DIR}")
    else:
        print("LatentSync already cloned.")

    # Create venv with Python 3.11 (mediapipe requires <=3.11)
    if not os.path.isdir(VENV_DIR):
        print("Creating venv...")
        run(f"uv venv --python 3.11 {VENV_DIR}")
    else:
        print("Venv already exists.")

    # Install deps
    print("Installing dependencies...")
    run(f"uv pip install --python {VENV_PYTHON} -r {REQUIREMENTS}")

    # Verify critical imports — retry individually if bulk install missed any
    verify_install()

    # Apply MPS patches
    print("Applying MPS patches...")
    run(f"{VENV_PYTHON} {PATCH_SCRIPT} {REPO_DIR}")

    # Download checkpoints
    ckpt_dir = os.path.join(REPO_DIR, "checkpoints")
    if not os.path.isdir(ckpt_dir) or not os.listdir(ckpt_dir):
        print("Downloading LatentSync v1.5 checkpoints...")
        run(
            f"{VENV_DIR}/bin/huggingface-cli download ByteDance/LatentSync-1.5 "
            f"--local-dir {ckpt_dir}"
        )
    else:
        print("Checkpoints already downloaded.")

    # Write marker only after everything verified
    with open(MARKER, "w") as f:
        f.write("ok\n")
    print("Setup complete.")


def benchmark():
    """Run a quick inference to measure per-step speed. Caches result.

    Uses the built-in demo files but only processes enough to time
    a few denoising steps. Result is cached to .benchmark.json
    and reused for all future time estimates.
    """
    if os.path.isfile(BENCHMARK_CACHE):
        with open(BENCHMARK_CACHE) as f:
            cached = json.load(f)
        print(f"Benchmark (cached): {cached['sec_per_step']:.1f}s/step on {cached['device']}")
        return cached

    # Use built-in demo files
    demo_video = os.path.join(REPO_DIR, "assets", "demo1_video.mp4")
    demo_audio = os.path.join(REPO_DIR, "assets", "demo1_audio.wav")
    if not os.path.isfile(demo_video) or not os.path.isfile(demo_audio):
        print("Benchmark: demo files not found, skipping.")
        return None

    print("Running benchmark (first-time only, ~2 min)...")
    config_path = os.path.join(REPO_DIR, "configs", "unet", "stage2_512.yaml")
    ckpt_path = os.path.join(REPO_DIR, "checkpoints", "latentsync_unet.pt")
    bench_output = os.path.join(BASE_DIR, ".bench_output.mp4")

    # Run 2-step inference on the demo clip
    # The first batch is always faster (cold GPU), so we run the full demo
    # to get a realistic average. With 2 steps this takes ~2-3 min.
    cmd = [
        VENV_PYTHON, "-m", "scripts.inference",
        "--unet_config_path", config_path,
        "--inference_ckpt_path", ckpt_path,
        "--video_path", demo_video,
        "--audio_path", demo_audio,
        "--video_out_path", bench_output,
        "--inference_steps", "2",
        "--guidance_scale", "1.0",
    ]

    t0 = time.time()
    result = subprocess.run(cmd, cwd=REPO_DIR)
    elapsed = time.time() - t0

    # Clean up benchmark output
    if os.path.isfile(bench_output):
        os.unlink(bench_output)

    if result.returncode != 0:
        print(f"Benchmark failed (exit {result.returncode})")
        return None

    # demo1 has 242 frames = 16 batches, 2 steps each
    # Subtract ~30s overhead (face detection, affine, restore, ffmpeg)
    overhead = 30
    denoise_time = max(elapsed - overhead, elapsed * 0.8)
    num_batches = 16
    bench_steps = 2
    sec_per_step = denoise_time / (num_batches * bench_steps)

    device = "mps"

    cached = {
        "sec_per_step": round(sec_per_step, 2),
        "device": device,
        "total_bench_sec": round(elapsed, 1),
        "bench_frames": 242,
        "bench_steps": bench_steps,
    }

    with open(BENCHMARK_CACHE, "w") as f:
        json.dump(cached, f, indent=2)

    print(f"Benchmark: {sec_per_step:.1f}s/step on {device} ({elapsed:.0f}s total)")
    return cached


def get_video_frame_count(video_path):
    """Get frame count from video using ffprobe."""
    try:
        result = subprocess.run(
            ["ffprobe", "-v", "error", "-select_streams", "v:0",
             "-count_packets", "-show_entries", "stream=nb_read_packets",
             "-of", "csv=p=0", video_path],
            capture_output=True, text=True, timeout=10,
        )
        if result.returncode == 0 and result.stdout.strip():
            return int(result.stdout.strip())
    except (subprocess.TimeoutExpired, ValueError):
        pass
    return None


# Default: ~18s/step on M4 Max (FP32, MPS). Updated by benchmark.
DEFAULT_SEC_PER_STEP = 18.0


def estimate_time(video_path, steps):
    """Estimate inference time. Uses benchmark cache if available, else default."""
    # Get sec_per_step from cache or default
    if os.path.isfile(BENCHMARK_CACHE):
        with open(BENCHMARK_CACHE) as f:
            cached = json.load(f)
        sec_per_step = cached["sec_per_step"]
    else:
        sec_per_step = DEFAULT_SEC_PER_STEP

    # Get frame count from video
    num_frames = get_video_frame_count(video_path)
    if num_frames is None:
        num_frames = 242  # fallback: ~10s at 25fps

    num_batches = (num_frames + 15) // 16  # 16 frames per batch
    total_sec = sec_per_step * num_batches * steps
    return total_sec


def inference(video, audio, output, steps=20, guidance=1.5):
    """Run LatentSync inference with MPS."""
    ensure_brew_path()

    if not os.path.isfile(MARKER):
        print("First run — running setup...")
        setup()

    if not os.path.isfile(VENV_PYTHON):
        print(f"Error: venv python not found at {VENV_PYTHON}")
        print("Run with --setup to reinstall.")
        sys.exit(1)

    # Run benchmark if no cached data (first-ever invocation)
    if not os.path.isfile(BENCHMARK_CACHE):
        benchmark()

    # Estimate time before starting
    est = estimate_time(video, steps)
    if est is not None:
        mins = est / 60
        print(f"Estimated time: ~{mins:.0f} min ({steps} steps)")

    env = os.environ.copy()
    # MPS fallback removed — all LatentSync ops work natively on MPS (verified by diagnose_mps.py).
    # The blanket fallback silently routes ALL ops to CPU, defeating GPU usage entirely.

    config_path = os.path.join(REPO_DIR, "configs", "unet", "stage2_512.yaml")
    ckpt_path = os.path.join(REPO_DIR, "checkpoints", "latentsync_unet.pt")

    cmd = [
        VENV_PYTHON, "-m", "scripts.inference",
        "--unet_config_path", config_path,
        "--inference_ckpt_path", ckpt_path,
        "--video_path", video,
        "--audio_path", audio,
        "--video_out_path", output,
        "--inference_steps", str(steps),
        "--guidance_scale", str(guidance),
    ]

    print(f"Running inference...")
    print(f"  video:    {video}")
    print(f"  audio:    {audio}")
    print(f"  output:   {output}")
    print(f"  steps:    {steps}")
    print(f"  guidance: {guidance}")

    t0 = time.time()
    result = subprocess.run(cmd, cwd=REPO_DIR, env=env)
    elapsed = time.time() - t0

    if result.returncode != 0:
        print(f"Inference failed (exit {result.returncode})")
        sys.exit(1)

    if os.path.isfile(output):
        size = os.path.getsize(output)
        print(f"Done. Output: {output} ({size} bytes, {elapsed:.0f}s)")
    else:
        print(f"Error: output file not created at {output}")
        sys.exit(1)


def main():
    parser = argparse.ArgumentParser(description="LatentSync lip-sync (MPS)")
    parser.add_argument("--setup", action="store_true", help="Run setup only")
    parser.add_argument("--diagnose", action="store_true", help="Run MPS GPU diagnostic")
    parser.add_argument("--benchmark", action="store_true", help="Run speed benchmark (uses demo files)")
    parser.add_argument("--video", help="Input face video")
    parser.add_argument("--audio", help="Input audio")
    parser.add_argument("--output", "-o", help="Output video path")
    parser.add_argument("--steps", type=int, default=20, help="Inference steps")
    parser.add_argument("--guidance", type=float, default=1.5, help="Guidance scale")
    args = parser.parse_args()

    if args.setup:
        setup()
        return

    if args.diagnose:
        diagnose_script = os.path.join(SCRIPT_DIR, "diagnose_mps.py")
        result = subprocess.run([VENV_PYTHON, diagnose_script])
        sys.exit(result.returncode)

    if args.benchmark:
        ensure_brew_path()
        if not os.path.isfile(MARKER):
            setup()
        # Delete cache to force re-run
        if os.path.isfile(BENCHMARK_CACHE):
            os.unlink(BENCHMARK_CACHE)
        benchmark()
        return

    if not args.video or not args.audio or not args.output:
        parser.error("--video, --audio, and --output are required for inference")

    inference(args.video, args.audio, args.output, args.steps, args.guidance)


if __name__ == "__main__":
    main()
