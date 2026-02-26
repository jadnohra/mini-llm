import json
import subprocess
import threading
import time
from datetime import datetime
from pathlib import Path

STATE_DIR = Path.home() / ".mini" / "cue"

# TTS message templates — varied to avoid sounding robotic
_TASK_START = [
    "Starting {task}.",
    "Time for {task}.",
]
_BREAK = [
    "Take a {dur} minute break.",
    "Break time, step away for {dur}.",
]
_RESUME = [
    "Break's over. Back to {task}.",
    "Ready to continue {task}.",
]
_TASK_END = [
    "{task} done. Next: {next_task}.",
    "Finished {task}. Moving on to {next_task}.",
]
_TASK_END_LAST = [
    "{task} done.",
    "Finished {task}.",
]
_DAY_END = [
    "All done for today.",
    "That's everything. Nice work.",
]


def _pick(templates, **kwargs):
    """Pick a template based on current second (deterministic but varied)."""
    idx = int(time.time()) % len(templates)
    return templates[idx].format(**kwargs)


def _say(text):
    """Speak text via mini say."""
    subprocess.run(["mini", "say", text], check=False)


def _minutes_until(time_str):
    """Minutes from now until HH:MM today. Negative if past."""
    now = datetime.now()
    h, m = time_str.split(":")
    target = now.replace(hour=int(h), minute=int(m), second=0, microsecond=0)
    return (target - now).total_seconds() / 60


class Runner:
    """Timer engine for a running schedule.

    Takes a flat event list from calculate_timeline and fires
    mini say notifications at each event time.
    """

    def __init__(self, schedule, on_input=None, say_fn=None):
        """
        Args:
            schedule: list of timeline events from calculate_timeline
            on_input: callback(text) for live user input; returns action dict or None
            say_fn: override for _say (for testing)
        """
        self.schedule = schedule
        self.on_input = on_input
        self.say = say_fn or _say
        self._timer = None
        self._stop_event = threading.Event()
        self._event_idx = 0
        self._paused = False

    def start(self):
        """Start the schedule runner."""
        STATE_DIR.mkdir(parents=True, exist_ok=True)
        self._save_state()
        self._arm_next()

    def stop(self):
        """Stop the runner."""
        self._stop_event.set()
        if self._timer:
            self._timer.cancel()

    def _arm_next(self):
        """Arm timer for the next event."""
        if self._stop_event.is_set():
            return
        if self._event_idx >= len(self.schedule):
            self.say(_pick(_DAY_END))
            self._save_state()
            return

        event = self.schedule[self._event_idx]
        delay = _minutes_until(event["time"]) * 60  # seconds

        if delay <= 0:
            # Event is in the past or now — fire immediately
            self._fire_event()
        else:
            self._timer = threading.Timer(delay, self._fire_event)
            self._timer.daemon = True
            self._timer.start()

    def _fire_event(self):
        """Execute the current event and arm the next."""
        if self._stop_event.is_set():
            return

        event = self.schedule[self._event_idx]
        etype = event["type"]

        if etype == "task_start":
            self.say(_pick(_TASK_START, task=event["task"]))
        elif etype == "break":
            dur = self._break_duration()
            self.say(_pick(_BREAK, dur=dur))
        elif etype == "resume":
            self.say(_pick(_RESUME, task=event["task"]))
        elif etype == "task_end":
            next_task = self._next_task_name()
            if next_task:
                self.say(_pick(_TASK_END, task=event["task"], next_task=next_task))
            else:
                self.say(_pick(_TASK_END_LAST, task=event["task"]))

        self._event_idx += 1
        self._save_state()
        self._arm_next()

    def _break_duration(self):
        """Get break duration by looking at time gap to next event."""
        if self._event_idx + 1 < len(self.schedule):
            cur = self.schedule[self._event_idx]
            nxt = self.schedule[self._event_idx + 1]
            cur_min = _parse_time(cur["time"])
            nxt_min = _parse_time(nxt["time"])
            return nxt_min - cur_min
        return 5

    def _next_task_name(self):
        """Find the next task_start after current position."""
        for i in range(self._event_idx + 1, len(self.schedule)):
            if self.schedule[i]["type"] == "task_start":
                return self.schedule[i]["task"]
        return None

    def handle_action(self, action):
        """Handle a live adjustment action dict."""
        act = action.get("action")

        if act == "stop":
            self.say("Stopping schedule.")
            self.stop()

        elif act == "pause":
            self._paused = True
            if self._timer:
                self._timer.cancel()
            self.say("Schedule paused.")

        elif act == "resume":
            self._paused = False
            self.say("Resuming schedule.")
            self._arm_next()

        elif act == "skip_break":
            # Skip forward past break + resume events
            while self._event_idx < len(self.schedule):
                etype = self.schedule[self._event_idx]["type"]
                if etype in ("break", "resume"):
                    self._event_idx += 1
                else:
                    break
            self.say("Skipping break.")
            if self._timer:
                self._timer.cancel()
            self._arm_next()

        elif act == "finish_early":
            # Skip to next task_start or end
            while self._event_idx < len(self.schedule):
                if self.schedule[self._event_idx]["type"] == "task_start":
                    break
                self._event_idx += 1
            if self._timer:
                self._timer.cancel()
            if self._event_idx < len(self.schedule):
                self.say(f"Moving on. {self.schedule[self._event_idx]['task']} is next.")
                self._fire_event()
            else:
                self.say(_pick(_DAY_END))

        elif act == "status":
            self.say(self._status_text())

        elif act == "extend":
            minutes = action.get("minutes", 10)
            self.say(f"Extended by {minutes} minutes.")
            # Shift all remaining events forward
            for i in range(self._event_idx, len(self.schedule)):
                old_min = _parse_time(self.schedule[i]["time"])
                self.schedule[i]["time"] = _fmt(old_min + minutes)
            if self._timer:
                self._timer.cancel()
            self._arm_next()

    def _status_text(self):
        """Build a status string about current position."""
        if self._event_idx >= len(self.schedule):
            return "Schedule is complete."
        cur = self.schedule[self._event_idx]
        return f"Next up: {cur['type']} at {cur['time']}."

    def _save_state(self):
        """Persist current state for resume."""
        state = {
            "schedule": self.schedule,
            "event_idx": self._event_idx,
            "saved_at": datetime.now().isoformat(),
        }
        state_file = STATE_DIR / "active.json"
        state_file.write_text(json.dumps(state, indent=2))

    @classmethod
    def load(cls, say_fn=None):
        """Resume from saved state."""
        state_file = STATE_DIR / "active.json"
        if not state_file.exists():
            return None
        state = json.loads(state_file.read_text())
        runner = cls(state["schedule"], say_fn=say_fn)
        runner._event_idx = state["event_idx"]
        return runner


def _parse_time(s):
    h, m = s.split(":")
    return int(h) * 60 + int(m)


def _fmt(minutes):
    return f"{minutes // 60:02d}:{minutes % 60:02d}"
