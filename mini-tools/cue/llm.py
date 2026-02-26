import json
from datetime import datetime
from pathlib import Path
from string import Template

import requests

from tools import TOOL_DEFS, TOOL_DISPATCH, _get_cue_config

PROMPTS_DIR = Path(__file__).parent / "prompts"
OLLAMA_URL = "http://localhost:11434"


def _load_prompt(name):
    return (PROMPTS_DIR / f"{name}.md").read_text()


def build_planner_system(config_path=None):
    """Assemble the planner system prompt from template + config."""
    cue = _get_cue_config(config_path)
    template = _load_prompt("planner")

    styles_text = ""
    for name, style in cue["work_styles"].items():
        desc = style.get("description", "")
        every = style.get("break_every_min", 0)
        dur = style.get("break_duration_min", 0)
        examples = ", ".join(style.get("examples", []))
        styles_text += f"- **{name}**: {desc}\n"
        if every > 0:
            styles_text += f"  Break every {every} min, {dur} min break\n"
        else:
            styles_text += f"  No breaks\n"
        if examples:
            styles_text += f"  Examples: {examples}\n"

    overrides = cue.get("task_overrides", {})
    overrides_text = ""
    if overrides:
        overrides_text = "Task overrides (pre-assigned styles):\n"
        for task_name, cfg in overrides.items():
            overrides_text += f"- \"{task_name}\" → {cfg['work_style']}\n"

    return Template(template).substitute(
        work_styles=styles_text,
        task_overrides=overrides_text,
        current_time=datetime.now().strftime("%H:%M"),
        default_start=cue.get("default_start", "09:00"),
        default_end=cue.get("default_end", "18:00"),
    )


def chat(model, messages, tools=None, ollama_url=None):
    """Single Ollama /api/chat call. Returns the response message dict."""
    url = (ollama_url or OLLAMA_URL) + "/api/chat"
    payload = {
        "model": model,
        "messages": messages,
        "stream": False,
    }
    if tools:
        payload["tools"] = tools
    resp = requests.post(url, json=payload, timeout=120)
    resp.raise_for_status()
    return resp.json()["message"]


def run_tool_calls(tool_calls, config_path=None):
    """Execute tool calls returned by the model. Returns list of tool result messages."""
    results = []
    for tc in tool_calls:
        fn_name = tc["function"]["name"]
        fn_args = tc["function"].get("arguments", {})
        fn = TOOL_DISPATCH.get(fn_name)
        if fn is None:
            result = {"error": f"unknown tool '{fn_name}'"}
        else:
            try:
                result = fn(fn_args, config_path=config_path)
            except TypeError:
                # current_time doesn't take config_path
                result = fn(fn_args)
        results.append({
            "role": "tool",
            "content": json.dumps(result),
        })
    return results


def planner_loop(user_text, messages=None, config_path=None, ollama_url=None):
    """Run one turn of the planning conversation.

    Takes user text, appends to message history, calls Ollama with tools
    in a loop until the model produces a text response or a start action.

    Returns (response_text, messages, schedule_or_none).
    - response_text: what to speak via mini say
    - messages: updated conversation history
    - schedule: if the user confirmed, the final schedule dict; else None
    """
    cue = _get_cue_config(config_path)
    model = cue.get("model_planner", "qwen3-coder:30b")

    if messages is None:
        system_prompt = build_planner_system(config_path)
        messages = [{"role": "system", "content": system_prompt}]

    messages.append({"role": "user", "content": user_text})

    while True:
        msg = chat(model, messages, tools=TOOL_DEFS, ollama_url=ollama_url)
        messages.append(msg)

        tool_calls = msg.get("tool_calls")

        # Fallback: some models put tool calls as JSON in content
        if not tool_calls:
            content = msg.get("content", "").strip()
            try:
                parsed_content = json.loads(content)
                if isinstance(parsed_content, dict) and "name" in parsed_content:
                    tool_calls = [{"function": parsed_content}]
            except (json.JSONDecodeError, KeyError):
                pass

        if tool_calls:
            tool_results = run_tool_calls(tool_calls, config_path)
            messages.extend(tool_results)
            continue

        # No tool calls — model produced a text response
        content = msg.get("content", "")

        # Check if it's a start action
        try:
            parsed = json.loads(content)
            if isinstance(parsed, dict) and parsed.get("action") == "start":
                return content, messages, parsed["schedule"]
        except (json.JSONDecodeError, KeyError):
            pass

        return content, messages, None


def adjuster_call(user_text, schedule_state, config_path=None, ollama_url=None):
    """Parse a live adjustment command via the fast model.

    Returns the parsed action dict, or None on parse failure.
    """
    cue = _get_cue_config(config_path)
    model = cue.get("model_live", "phi4-mini:latest")
    system_prompt = _load_prompt("adjuster")

    messages = [
        {"role": "system", "content": system_prompt},
        {"role": "user", "content": f"Schedule state:\n{json.dumps(schedule_state)}\n\nUser: {user_text}"},
    ]

    msg = chat(model, messages, ollama_url=ollama_url)
    content = msg.get("content", "")

    try:
        return json.loads(content)
    except json.JSONDecodeError:
        return None
