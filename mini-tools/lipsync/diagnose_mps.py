#!/usr/bin/env python3
"""Diagnose MPS GPU support for LatentSync ops.

Runs each potentially problematic op on MPS (without PYTORCH_ENABLE_MPS_FALLBACK)
to identify exactly which ops fail and need targeted CPU fallbacks.

Usage (on Mac Mini):
  ~/.mini/lipsync/venv/bin/python ~/mini-llm/mini-tools/lipsync/diagnose_mps.py
"""

import os
import sys
import traceback

# Ensure MPS fallback is OFF so we can see real failures
os.environ.pop("PYTORCH_ENABLE_MPS_FALLBACK", None)

import torch
import torch.nn.functional as F


def test(name, fn):
    """Run fn on MPS, report pass/fail."""
    try:
        result = fn()
        # Force sync to catch async MPS errors
        if isinstance(result, torch.Tensor):
            result.cpu()
        torch.mps.synchronize()
        print(f"  PASS  {name}")
        return True
    except Exception as e:
        print(f"  FAIL  {name}: {e}")
        return False


def main():
    print("=== MPS Diagnostic for LatentSync ===\n")

    # Basic info
    print(f"PyTorch:       {torch.__version__}")
    print(f"MPS available: {torch.backends.mps.is_available()}")
    print(f"MPS built:     {torch.backends.mps.is_built()}")
    if not torch.backends.mps.is_available():
        print("MPS not available — nothing to diagnose.")
        sys.exit(1)

    device = torch.device("mps")
    print(f"Device:        {device}\n")

    results = {}

    # --- Core tensor ops ---
    print("[Core ops]")

    results["conv2d"] = test("Conv2d (UNet backbone)", lambda: (
        torch.nn.Conv2d(320, 320, 3, padding=1).to(device)(
            torch.randn(1, 320, 64, 64, device=device)
        )
    ))

    results["linear"] = test("Linear (attention)", lambda: (
        torch.nn.Linear(320, 320).to(device)(
            torch.randn(1, 4096, 320, device=device)
        )
    ))

    results["group_norm"] = test("GroupNorm", lambda: (
        torch.nn.GroupNorm(32, 320).to(device)(
            torch.randn(1, 320, 64, 64, device=device)
        )
    ))

    # --- F.interpolate (the known problem area) ---
    print("\n[F.interpolate]")

    results["interp_4d_nearest"] = test(
        "F.interpolate 4D nearest (standard upscale)", lambda: (
            F.interpolate(
                torch.randn(1, 320, 32, 32, device=device),
                scale_factor=2.0, mode="nearest"
            )
        ))

    results["interp_5d_nearest_scale"] = test(
        "F.interpolate 5D nearest scale_factor=[1,2,2] (resnet upsample)", lambda: (
            F.interpolate(
                torch.randn(2, 320, 1, 32, 32, device=device),
                scale_factor=[1.0, 2.0, 2.0], mode="nearest"
            )
        ))

    results["interp_5d_nearest_size"] = test(
        "F.interpolate 5D nearest size (resnet downsample)", lambda: (
            F.interpolate(
                torch.randn(2, 320, 1, 64, 64, device=device),
                size=(1, 32, 32), mode="nearest"
            )
        ))

    results["interp_5d_nearest_f32"] = test(
        "F.interpolate 5D nearest (float32 workaround)", lambda: (
            F.interpolate(
                torch.randn(2, 320, 1, 32, 32, device=device).to(torch.float32),
                scale_factor=[1.0, 2.0, 2.0], mode="nearest"
            )
        ))

    # --- torch.stft (Whisper mel spectrogram) ---
    print("\n[Whisper / audio]")

    results["stft"] = test("torch.stft (mel spectrogram)", lambda: (
        torch.stft(
            torch.randn(480000, device=device),
            n_fft=400, hop_length=160, win_length=400,
            window=torch.hann_window(400, device=device),
            return_complex=True,
        )
    ))

    # --- grid_sample / affine_grid (face alignment) ---
    print("\n[Face alignment]")

    results["affine_grid"] = test("affine_grid", lambda: (
        F.affine_grid(
            torch.eye(2, 3, device=device).unsqueeze(0),
            [1, 3, 256, 256],
            align_corners=False,
        )
    ))

    def _grid_sample():
        img = torch.randn(1, 3, 256, 256, device=device)
        theta = torch.eye(2, 3, device=device).unsqueeze(0)
        grid = F.affine_grid(theta, img.shape, align_corners=False)
        return F.grid_sample(img, grid, align_corners=False)

    results["grid_sample"] = test("grid_sample (face warp)", _grid_sample)

    # --- VAE ops ---
    print("\n[VAE-like ops]")

    results["conv2d_large"] = test("Conv2d large (VAE decoder)", lambda: (
        torch.nn.Conv2d(512, 512, 3, padding=1).to(device)(
            torch.randn(1, 512, 64, 64, device=device)
        )
    ))

    results["silu"] = test("SiLU activation", lambda: (
        torch.nn.SiLU()(torch.randn(1, 512, 64, 64, device=device))
    ))

    # --- Attention ---
    print("\n[Attention]")

    results["sdpa"] = test("scaled_dot_product_attention", lambda: (
        F.scaled_dot_product_attention(
            torch.randn(1, 8, 4096, 40, device=device),
            torch.randn(1, 8, 4096, 40, device=device),
            torch.randn(1, 8, 4096, 40, device=device),
        )
    ))

    results["bmm"] = test("torch.bmm (attention matmul)", lambda: (
        torch.bmm(
            torch.randn(8, 4096, 40, device=device),
            torch.randn(8, 40, 4096, device=device),
        )
    ))

    # --- Summary ---
    print("\n=== Summary ===")
    passed = sum(1 for v in results.values() if v)
    total = len(results)
    print(f"{passed}/{total} tests passed\n")

    failed = [k for k, v in results.items() if not v]
    if failed:
        print("FAILED ops (need targeted CPU fallback):")
        for k in failed:
            print(f"  - {k}")
    else:
        print("All ops work on MPS! The blanket fallback can be removed.")
        print("If GPU still shows 0%, the issue is elsewhere (tensor not on MPS device).")

    return 0 if not failed else 1


if __name__ == "__main__":
    sys.exit(main())
