"""Cue — voice-first scheduling assistant.

Main entry point: planning conversation → live schedule runner.
Input via stdin (dev/testing) or HTTP endpoint (production).
"""
import sys

from llm import planner_loop, adjuster_call
from runner import Runner


def _say_print(text):
    """Fallback say: just print. Real deploy uses mini say via runner."""
    import subprocess
    subprocess.run(["mini", "say", text], check=False)


def planning_phase(config_path=None, ollama_url=None):
    """Interactive planning conversation. Returns the final schedule."""
    messages = None
    print("Cue is ready. Describe your day.\n")

    while True:
        try:
            user_input = input("> ")
        except (EOFError, KeyboardInterrupt):
            print("\nBye.")
            return None

        if not user_input.strip():
            continue

        text, messages, schedule = planner_loop(
            user_input, messages=messages,
            config_path=config_path, ollama_url=ollama_url,
        )

        if schedule is not None:
            print(f"\nCue: {text}")
            return schedule

        print(f"\nCue: {text}\n")


def live_phase(schedule, config_path=None, ollama_url=None):
    """Run the schedule with live adjustments."""
    runner = Runner(schedule, say_fn=_say_print)
    runner.start()

    while not runner._stop_event.is_set():
        try:
            user_input = input()
        except (EOFError, KeyboardInterrupt):
            runner.stop()
            break

        if not user_input.strip():
            continue

        schedule_state = {
            "schedule": runner.schedule,
            "event_idx": runner._event_idx,
        }
        action = adjuster_call(
            user_input, schedule_state,
            config_path=config_path, ollama_url=ollama_url,
        )
        if action:
            runner.handle_action(action)
        else:
            _say_print("Sorry, I didn't understand that.")


def main():
    config_path = None
    ollama_url = None

    # Simple arg parsing
    args = sys.argv[1:]
    for i, arg in enumerate(args):
        if arg == "--config" and i + 1 < len(args):
            config_path = args[i + 1]
        elif arg == "--ollama" and i + 1 < len(args):
            ollama_url = args[i + 1]

    schedule = planning_phase(config_path=config_path, ollama_url=ollama_url)
    if schedule is None:
        return

    live_phase(schedule, config_path=config_path, ollama_url=ollama_url)


if __name__ == "__main__":
    main()
