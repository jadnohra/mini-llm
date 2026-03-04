#!/usr/bin/env python3
"""LatentSync wrapper for MPS (Apple Silicon).

Setup: clones LatentSync, creates venv, installs deps, patches for MPS,
       downloads v1.5 checkpoints.
Inference: runs patched inference.py with PYTORCH_ENABLE_MPS_FALLBACK=1.
"""

import argparse
import os
import subprocess
import sys

BASE_DIR = os.path.expanduser("~/.mini/lipsync")
REPO_DIR = os.path.join(BASE_DIR, "LatentSync")
VENV_DIR = os.path.join(BASE_DIR, "venv")
VENV_PYTHON = os.path.join(VENV_DIR, "bin", "python")
MARKER = os.path.join(BASE_DIR, ".setup-done")
SCRIPT_DIR = os.path.dirname(os.path.abspath(__file__))
REQUIREMENTS = os.path.join(SCRIPT_DIR, "requirements-mps.txt")
PATCH_SCRIPT = os.path.join(SCRIPT_DIR, "patch_mps.py")

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
    """First-time setup: clone, venv, deps, patch, checkpoints."""
    os.makedirs(BASE_DIR, exist_ok=True)

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


def inference(video, audio, output, steps=20, guidance=1.5):
    """Run LatentSync inference with MPS."""
    if not os.path.isfile(MARKER):
        print("First run — running setup...")
        setup()

    if not os.path.isfile(VENV_PYTHON):
        print(f"Error: venv python not found at {VENV_PYTHON}")
        print("Run with --setup to reinstall.")
        sys.exit(1)

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

    result = subprocess.run(cmd, cwd=REPO_DIR, env=env)
    if result.returncode != 0:
        print(f"Inference failed (exit {result.returncode})")
        sys.exit(1)

    if os.path.isfile(output):
        size = os.path.getsize(output)
        print(f"Done. Output: {output} ({size} bytes)")
    else:
        print(f"Error: output file not created at {output}")
        sys.exit(1)


def main():
    parser = argparse.ArgumentParser(description="LatentSync lip-sync (MPS)")
    parser.add_argument("--setup", action="store_true", help="Run setup only")
    parser.add_argument("--diagnose", action="store_true", help="Run MPS GPU diagnostic")
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

    if not args.video or not args.audio or not args.output:
        parser.error("--video, --audio, and --output are required for inference")

    inference(args.video, args.audio, args.output, args.steps, args.guidance)


if __name__ == "__main__":
    main()
