import os
from datetime import datetime
from pathlib import Path

import yaml

from timeline import calculate_timeline as _calculate_timeline

CONFIG_PATH = Path.home() / ".mini" / "config.yaml"


def _default_work_styles():
    return {
        "deep": {
            "description": "Deep technical work requiring sustained focus",
            "break_every_min": 25,
            "break_duration_min": 5,
            "examples": ["coding", "debugging", "refactoring", "system design"],
        },
        "learning": {
            "description": "Studying, reading docs, following tutorials",
            "break_every_min": 20,
            "break_duration_min": 5,
            "examples": ["reading docs", "tutorial", "course", "studying"],
        },
        "exploration": {
            "description": "Exploratory work: experimenting, tinkering, setup",
            "break_every_min": 30,
            "break_duration_min": 5,
            "examples": ["experimenting", "setting up", "prototyping"],
        },
        "admin": {
            "description": "Low-focus admin: emails, PRs, issues, updates",
            "break_every_min": 0,
            "break_duration_min": 0,
            "examples": ["email", "PR review", "issues", "updates"],
        },
    }


def _load_config(config_path=None):
    path = Path(config_path) if config_path else CONFIG_PATH
    if not path.exists():
        return {}
    with open(path) as f:
        return yaml.safe_load(f) or {}


def _save_config(data, config_path=None):
    path = Path(config_path) if config_path else CONFIG_PATH
    path.parent.mkdir(parents=True, exist_ok=True)
    with open(path, "w") as f:
        yaml.safe_dump(data, f, default_flow_style=False, sort_keys=False)


def _get_cue_config(config_path=None):
    data = _load_config(config_path)
    cue = data.get("cue", {})
    if "work_styles" not in cue:
        cue["work_styles"] = _default_work_styles()
    return cue


def calculate_timeline(params, config_path=None):
    cue = _get_cue_config(config_path)
    work_styles = cue["work_styles"]
    task_overrides = cue.get("task_overrides", {})

    tasks = params["tasks"]
    for task in tasks:
        if "work_style" not in task and task["name"] in task_overrides:
            task["work_style"] = task_overrides[task["name"]]["work_style"]

    return _calculate_timeline(
        tasks,
        work_styles,
        params["start_time"],
        end_time=params.get("end_time"),
    )


def manage_work_styles(params, config_path=None):
    action = params["action"]

    if action == "list":
        cue = _get_cue_config(config_path)
        return {"styles": cue["work_styles"]}

    data = _load_config(config_path)
    cue = data.setdefault("cue", {})
    if "work_styles" not in cue:
        cue["work_styles"] = _default_work_styles()

    if action == "create":
        name = params["name"]
        if name in cue["work_styles"]:
            return {"ok": False, "error": f"style '{name}' already exists"}
        cue["work_styles"][name] = params["style"]
        _save_config(data, config_path)
        return {"ok": True, "styles": list(cue["work_styles"].keys())}

    elif action == "update":
        name = params["name"]
        if name not in cue["work_styles"]:
            return {"ok": False, "error": f"style '{name}' not found"}
        cue["work_styles"][name].update(params["style"])
        _save_config(data, config_path)
        return {"ok": True}

    elif action == "delete":
        name = params["name"]
        if name not in cue["work_styles"]:
            return {"ok": False, "error": f"style '{name}' not found"}
        del cue["work_styles"][name]
        _save_config(data, config_path)
        return {"ok": True}

    return {"ok": False, "error": f"unknown action '{action}'"}


def current_time(params=None, **kwargs):
    return {"now": datetime.now().strftime("%H:%M")}


TOOL_DEFS = [
    {
        "type": "function",
        "function": {
            "name": "calculate_timeline",
            "description": "Compute a full schedule timeline with breaks inserted per work style. Returns event list, totals, end time, and whether it fits the time window.",
            "parameters": {
                "type": "object",
                "required": ["tasks", "start_time"],
                "properties": {
                    "tasks": {
                        "type": "array",
                        "description": "List of tasks to schedule",
                        "items": {
                            "type": "object",
                            "required": ["name", "duration_min"],
                            "properties": {
                                "name": {"type": "string"},
                                "duration_min": {"type": "integer"},
                                "work_style": {
                                    "type": "string",
                                    "description": "Work style name. If omitted, resolved from task_overrides config.",
                                },
                            },
                        },
                    },
                    "start_time": {
                        "type": "string",
                        "description": "Start time in HH:MM format",
                    },
                    "end_time": {
                        "type": "string",
                        "description": "Optional end time constraint in HH:MM format",
                    },
                },
            },
        },
    },
    {
        "type": "function",
        "function": {
            "name": "manage_work_styles",
            "description": "List, create, update, or delete work style profiles that control break patterns.",
            "parameters": {
                "type": "object",
                "required": ["action"],
                "properties": {
                    "action": {
                        "type": "string",
                        "enum": ["list", "create", "update", "delete"],
                    },
                    "name": {
                        "type": "string",
                        "description": "Style name (required for create/update/delete)",
                    },
                    "style": {
                        "type": "object",
                        "description": "Style definition (for create/update)",
                        "properties": {
                            "description": {"type": "string"},
                            "break_every_min": {"type": "integer"},
                            "break_duration_min": {"type": "integer"},
                            "examples": {
                                "type": "array",
                                "items": {"type": "string"},
                            },
                        },
                    },
                },
            },
        },
    },
    {
        "type": "function",
        "function": {
            "name": "current_time",
            "description": "Get the current time.",
            "parameters": {
                "type": "object",
                "properties": {},
            },
        },
    },
]

TOOL_DISPATCH = {
    "calculate_timeline": calculate_timeline,
    "manage_work_styles": manage_work_styles,
    "current_time": current_time,
}
