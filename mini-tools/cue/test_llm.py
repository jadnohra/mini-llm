import json
from unittest.mock import patch, MagicMock

import yaml

from llm import build_planner_system, run_tool_calls, planner_loop, adjuster_call


def _write_config(tmp_path, cue_config):
    path = tmp_path / "config.yaml"
    data = {"cue": cue_config}
    path.write_text(yaml.safe_dump(data, default_flow_style=False, sort_keys=False))
    return str(path)


def _base_config():
    return {
        "model_planner": "qwen2.5-coder:32b",
        "model_live": "phi4-mini:latest",
        "default_start": "09:00",
        "default_end": "18:00",
        "work_styles": {
            "learning": {
                "description": "Studying",
                "break_every_min": 20,
                "break_duration_min": 5,
                "examples": ["reading docs", "tutorial"],
            },
            "admin": {
                "description": "Low-focus admin",
                "break_every_min": 0,
                "break_duration_min": 0,
                "examples": ["email"],
            },
        },
        "task_overrides": {
            "linux skills": {"work_style": "learning"},
        },
    }


# --- build_planner_system ---


def test_build_planner_system(tmp_path):
    cfg = _write_config(tmp_path, _base_config())
    prompt = build_planner_system(config_path=cfg)
    assert "learning" in prompt
    assert "admin" in prompt
    assert "linux skills" in prompt
    assert "09:00" in prompt
    assert "18:00" in prompt


# --- run_tool_calls ---


def test_run_tool_calls_current_time(tmp_path):
    cfg = _write_config(tmp_path, _base_config())
    tool_calls = [{"function": {"name": "current_time", "arguments": {}}}]
    results = run_tool_calls(tool_calls, config_path=cfg)
    assert len(results) == 1
    assert results[0]["role"] == "tool"
    parsed = json.loads(results[0]["content"])
    assert "now" in parsed


def test_run_tool_calls_calculate_timeline(tmp_path):
    cfg = _write_config(tmp_path, _base_config())
    tool_calls = [
        {
            "function": {
                "name": "calculate_timeline",
                "arguments": {
                    "tasks": [{"name": "study", "duration_min": 40, "work_style": "learning"}],
                    "start_time": "10:00",
                },
            }
        }
    ]
    results = run_tool_calls(tool_calls, config_path=cfg)
    parsed = json.loads(results[0]["content"])
    assert parsed["total_work_min"] == 40
    assert parsed["total_break_min"] == 5


def test_run_tool_calls_unknown():
    tool_calls = [{"function": {"name": "bogus", "arguments": {}}}]
    results = run_tool_calls(tool_calls)
    parsed = json.loads(results[0]["content"])
    assert "error" in parsed


# --- planner_loop (mocked Ollama) ---


def _mock_chat_text(content):
    """Returns a mock chat function that returns a text response."""
    def fake_chat(model, messages, tools=None, ollama_url=None):
        return {"role": "assistant", "content": content}
    return fake_chat


def _mock_chat_tool_then_text(tool_call, final_text):
    """First call returns tool_calls, second returns text."""
    calls = iter([
        {"role": "assistant", "content": "", "tool_calls": [tool_call]},
        {"role": "assistant", "content": final_text},
    ])
    def fake_chat(model, messages, tools=None, ollama_url=None):
        return next(calls)
    return fake_chat


@patch("llm.chat")
def test_planner_loop_text_response(mock_chat, tmp_path):
    cfg = _write_config(tmp_path, _base_config())
    mock_chat.side_effect = _mock_chat_text("Sure, let me check that.")

    text, messages, schedule = planner_loop(
        "I want to study linux for an hour", config_path=cfg
    )
    assert text == "Sure, let me check that."
    assert schedule is None
    assert len(messages) == 3  # system + user + assistant


@patch("llm.chat")
def test_planner_loop_tool_call(mock_chat, tmp_path):
    cfg = _write_config(tmp_path, _base_config())
    tool_call = {
        "function": {
            "name": "calculate_timeline",
            "arguments": {
                "tasks": [{"name": "study", "duration_min": 60, "work_style": "learning"}],
                "start_time": "09:00",
            },
        }
    }
    mock_chat.side_effect = _mock_chat_tool_then_text(
        tool_call, "That's 60 min work plus 10 min breaks."
    )

    text, messages, schedule = planner_loop(
        "I want to study for an hour", config_path=cfg
    )
    assert "60 min" in text
    assert schedule is None
    # system + user + assistant(tool_call) + tool_result + assistant(text)
    assert len(messages) == 5


@patch("llm.chat")
def test_planner_loop_start_action(mock_chat, tmp_path):
    cfg = _write_config(tmp_path, _base_config())
    schedule_data = [{"type": "task_start", "task": "study", "time": "09:00"}]
    start_json = json.dumps({"action": "start", "schedule": schedule_data})
    mock_chat.side_effect = _mock_chat_text(start_json)

    text, messages, schedule = planner_loop("go", config_path=cfg)
    assert schedule == schedule_data


# --- adjuster_call (mocked Ollama) ---


@patch("llm.chat")
def test_adjuster_call_finish_early(mock_chat, tmp_path):
    cfg = _write_config(tmp_path, _base_config())
    mock_chat.return_value = {
        "role": "assistant",
        "content": '{"action": "finish_early"}',
    }

    result = adjuster_call("I'm done", {"current_task": "study"}, config_path=cfg)
    assert result == {"action": "finish_early"}


@patch("llm.chat")
def test_adjuster_call_parse_failure(mock_chat, tmp_path):
    cfg = _write_config(tmp_path, _base_config())
    mock_chat.return_value = {
        "role": "assistant",
        "content": "I don't understand",
    }

    result = adjuster_call("mumble", {"current_task": "study"}, config_path=cfg)
    assert result is None
