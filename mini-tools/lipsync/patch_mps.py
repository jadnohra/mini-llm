#!/usr/bin/env python3
"""Patch a cloned LatentSync repo for MPS (Apple Silicon) support."""

import argparse
import glob
import os
import sys


def patch_file(path, replacements):
    """Apply a list of (old, new) string replacements to a file.
    Idempotent: if the new pattern is already present, skip silently."""
    if not os.path.exists(path):
        print(f"  SKIP {path} (not found)")
        return False
    with open(path, "r") as f:
        content = f.read()
    original = content
    for old, new in replacements:
        if new in content:
            continue  # already patched
        if old not in content:
            print(f"  WARN {path}: pattern not found: {old[:60]}...")
            continue
        content = content.replace(old, new)
    if content == original:
        print(f"  OK   {path} (already patched)")
        return True
    with open(path, "w") as f:
        f.write(content)
    print(f"  OK   {path}")
    return True


def patch_inference(repo_dir):
    """Patch scripts/inference.py for MPS device detection + FP32."""
    path = os.path.join(repo_dir, "scripts", "inference.py")
    replacements = [
        # Device detection: CUDA-only → MPS/CUDA/CPU
        (
            'is_fp16_supported = torch.cuda.is_available() and torch.cuda.get_device_capability()[0] > 7\n'
            '    dtype = torch.float16 if is_fp16_supported else torch.float32',
            'if torch.backends.mps.is_available():\n'
            '        device = torch.device("mps")\n'
            '        weight_dtype = torch.float32\n'
            '    elif torch.cuda.is_available():\n'
            '        device = torch.device("cuda")\n'
            '        weight_dtype = torch.float16 if torch.cuda.get_device_capability()[0] > 7 else torch.float32\n'
            '    else:\n'
            '        device = torch.device("cpu")\n'
            '        weight_dtype = torch.float32\n'
            '    dtype = weight_dtype',
        ),
        # Audio encoder device: hardcoded "cuda" → variable
        (
            'device="cuda"',
            'device=device',
        ),
        # Pipeline device: hardcoded "cuda" → variable
        (
            'torch_device="cuda"',
            'torch_device=device',
        ),
        (
            '.to("cuda")',
            '.to(device)',
        ),
    ]
    return patch_file(path, replacements)


def patch_pipeline(repo_dir):
    """Patch lipsync_pipeline.py: ImageProcessor device."""
    path = os.path.join(repo_dir, "latentsync", "pipelines", "lipsync_pipeline.py")
    replacements = [
        (
            'device="cuda"',
            'device=self._execution_device',
        ),
    ]
    return patch_file(path, replacements)


def patch_resnet(repo_dir):
    """Patch resnet.py: F.interpolate float32 cast to avoid MPS black frames."""
    path = os.path.join(repo_dir, "latentsync", "models", "resnet.py")
    replacements = [
        (
            'hidden_states = F.interpolate(hidden_states, scale_factor=[1.0, 2.0, 2.0], mode="nearest")',
            '_orig_dtype = hidden_states.dtype\n'
            '            hidden_states = F.interpolate(\n'
            '                hidden_states.to(torch.float32),\n'
            '                scale_factor=[1.0, 2.0, 2.0], mode="nearest"\n'
            '            ).to(_orig_dtype)',
        ),
    ]
    return patch_file(path, replacements)


def patch_decord_imports(repo_dir):
    """Replace 'decord' imports with 'eva_decord' throughout the codebase.
    Idempotent: skips files already using eva_decord."""
    count = 0
    for ext in ("*.py",):
        for path in glob.glob(os.path.join(repo_dir, "**", ext), recursive=True):
            with open(path, "r") as f:
                content = f.read()
            if "from decord" not in content and "import decord" not in content:
                continue
            new_content = content.replace("from decord", "from eva_decord")
            new_content = new_content.replace("import decord", "import eva_decord as decord")
            if new_content != content:
                with open(path, "w") as f:
                    f.write(new_content)
                print(f"  OK   {path} (decord → eva_decord)")
                count += 1
    print(f"  decord→eva_decord: {count} files patched (0 = already done)")
    return count


def main():
    parser = argparse.ArgumentParser(description="Patch LatentSync for MPS")
    parser.add_argument("repo_dir", help="Path to cloned LatentSync repo")
    args = parser.parse_args()

    repo_dir = os.path.expanduser(args.repo_dir)
    if not os.path.isdir(repo_dir):
        print(f"Error: {repo_dir} is not a directory")
        sys.exit(1)

    print(f"Patching LatentSync at {repo_dir} for MPS...")

    ok = True
    ok &= patch_inference(repo_dir)
    ok &= patch_pipeline(repo_dir)
    ok &= patch_resnet(repo_dir)
    patch_decord_imports(repo_dir)

    if ok:
        print("Done.")
    else:
        print("Some patches may not have applied. Check warnings above.")
        sys.exit(1)


if __name__ == "__main__":
    main()
