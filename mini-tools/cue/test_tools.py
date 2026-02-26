import re

import yaml

from tools import calculate_timeline, manage_work_styles, current_time


def _write_config(tmp_path, cue_config):
    path = tmp_path / "config.yaml"
    data = {"cue": cue_config}
    path.write_text(yaml.safe_dump(data, default_flow_style=False, sort_keys=False))
    return str(path)


def _base_config():
    return {
        "work_styles": {
            "learning": {"break_every_min": 20, "break_duration_min": 5},
            "deep": {"break_every_min": 25, "break_duration_min": 5},
            "admin": {"break_every_min": 0, "break_duration_min": 0},
        },
        "task_overrides": {
            "linux skills": {"work_style": "learning"},
            "mini-llm": {"work_style": "deep"},
        },
    }


# --- calculate_timeline ---


def test_timeline_basic(tmp_path):
    cfg = _write_config(tmp_path, _base_config())
    result = calculate_timeline(
        {
            "tasks": [{"name": "study", "duration_min": 60, "work_style": "learning"}],
            "start_time": "09:00",
        },
        config_path=cfg,
    )
    assert result["total_work_min"] == 60
    assert result["total_break_min"] == 10
    assert result["end_time"] == "10:10"


def test_timeline_task_override(tmp_path):
    """Tasks without explicit work_style get resolved from task_overrides."""
    cfg = _write_config(tmp_path, _base_config())
    result = calculate_timeline(
        {
            "tasks": [{"name": "linux skills", "duration_min": 40}],
            "start_time": "10:00",
        },
        config_path=cfg,
    )
    # learning style: 20 on / 5 off → 20 + break + 20 = 45 min
    assert result["total_work_min"] == 40
    assert result["total_break_min"] == 5
    assert result["total_min"] == 45


def test_timeline_end_time(tmp_path):
    cfg = _write_config(tmp_path, _base_config())
    result = calculate_timeline(
        {
            "tasks": [{"name": "study", "duration_min": 60, "work_style": "learning"}],
            "start_time": "09:00",
            "end_time": "10:00",
        },
        config_path=cfg,
    )
    assert result["fits"] is False
    assert result["overflow_min"] == 10


def test_timeline_defaults_when_no_config(tmp_path):
    """Missing config file → uses default work styles."""
    cfg = str(tmp_path / "nonexistent.yaml")
    result = calculate_timeline(
        {
            "tasks": [{"name": "study", "duration_min": 40, "work_style": "learning"}],
            "start_time": "09:00",
        },
        config_path=cfg,
    )
    # Default learning: 20 on / 5 off → 20 + break + 20 = 45
    assert result["total_work_min"] == 40
    assert result["total_break_min"] == 5


# --- manage_work_styles ---


def test_styles_list(tmp_path):
    cfg = _write_config(tmp_path, _base_config())
    result = manage_work_styles({"action": "list"}, config_path=cfg)
    assert "learning" in result["styles"]
    assert "deep" in result["styles"]
    assert "admin" in result["styles"]


def test_styles_create(tmp_path):
    cfg = _write_config(tmp_path, _base_config())
    result = manage_work_styles(
        {
            "action": "create",
            "name": "pomodoro",
            "style": {"break_every_min": 25, "break_duration_min": 5},
        },
        config_path=cfg,
    )
    assert result["ok"] is True
    assert "pomodoro" in result["styles"]

    # Verify persisted
    listed = manage_work_styles({"action": "list"}, config_path=cfg)
    assert "pomodoro" in listed["styles"]


def test_styles_create_duplicate(tmp_path):
    cfg = _write_config(tmp_path, _base_config())
    result = manage_work_styles(
        {
            "action": "create",
            "name": "learning",
            "style": {"break_every_min": 30, "break_duration_min": 10},
        },
        config_path=cfg,
    )
    assert result["ok"] is False
    assert "already exists" in result["error"]


def test_styles_update(tmp_path):
    cfg = _write_config(tmp_path, _base_config())
    result = manage_work_styles(
        {"action": "update", "name": "learning", "style": {"break_every_min": 30}},
        config_path=cfg,
    )
    assert result["ok"] is True

    listed = manage_work_styles({"action": "list"}, config_path=cfg)
    assert listed["styles"]["learning"]["break_every_min"] == 30
    # Original field preserved
    assert listed["styles"]["learning"]["break_duration_min"] == 5


def test_styles_update_missing(tmp_path):
    cfg = _write_config(tmp_path, _base_config())
    result = manage_work_styles(
        {"action": "update", "name": "nope", "style": {"break_every_min": 10}},
        config_path=cfg,
    )
    assert result["ok"] is False
    assert "not found" in result["error"]


def test_styles_delete(tmp_path):
    cfg = _write_config(tmp_path, _base_config())
    result = manage_work_styles(
        {"action": "delete", "name": "admin"}, config_path=cfg
    )
    assert result["ok"] is True

    listed = manage_work_styles({"action": "list"}, config_path=cfg)
    assert "admin" not in listed["styles"]


def test_styles_delete_missing(tmp_path):
    cfg = _write_config(tmp_path, _base_config())
    result = manage_work_styles(
        {"action": "delete", "name": "nope"}, config_path=cfg
    )
    assert result["ok"] is False
    assert "not found" in result["error"]


# --- current_time ---


def test_current_time():
    result = current_time()
    assert re.match(r"^\d{2}:\d{2}$", result["now"])
