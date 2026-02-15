"""Smoke tests for llmtts. First run downloads models (~80 MB)."""
import tempfile
from pathlib import Path

from llmtts import tts, preprocess_for_tts


def test_inline_text(tmp_path):
    """Inline text produces a non-empty mp3."""
    input_file = tmp_path / "input.txt"
    input_file.write_text("hello")
    output_file = tmp_path / "output.mp3"

    tts(input_file, output_file=output_file)

    assert output_file.exists()
    data = output_file.read_bytes()
    assert len(data) > 100
    # Valid mp3 starts with ID3 tag or MPEG sync (0xFF followed by 0xE0+ mask)
    assert data[:3] == b"ID3" or (data[0] == 0xFF and data[1] & 0xE0 == 0xE0)


def test_file_input(tmp_path):
    """File input converts to mp3."""
    input_file = tmp_path / "test.txt"
    input_file.write_text("The quick brown fox jumps over the lazy dog.")
    output_file = tmp_path / "test.mp3"

    tts(input_file, output_file=output_file)

    assert output_file.exists()
    assert output_file.stat().st_size > 100


def test_readable_mode(tmp_path):
    """Readable mode handles numbers and hex without error."""
    input_file = tmp_path / "code.txt"
    input_file.write_text("Address 0x3a7b has 19,000,000 entries and 2048 bytes.")
    output_file = tmp_path / "code.mp3"

    tts(input_file, output_file=output_file, readable=True)

    assert output_file.exists()
    assert output_file.stat().st_size > 100


def test_preprocess():
    """Preprocessing converts numbers and hex to words."""
    result = preprocess_for_tts("0x3a7b has 19,000,000 items")
    assert "hash" in result
    assert "nineteen million" in result
    assert "0x" not in result
