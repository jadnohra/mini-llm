import json

from runner import Runner, _parse_time, _fmt


def _sample_schedule():
    """A simple schedule: one task with one break."""
    return [
        {"type": "task_start", "task": "study", "time": "09:00"},
        {"type": "break", "time": "09:20"},
        {"type": "resume", "task": "study", "time": "09:25"},
        {"type": "task_end", "task": "study", "time": "09:45"},
    ]


# --- helpers ---


def test_parse_time():
    assert _parse_time("09:00") == 540
    assert _parse_time("13:30") == 810


def test_fmt():
    assert _fmt(540) == "09:00"
    assert _fmt(810) == "13:30"


# --- Runner with mock say (synchronous firing) ---


def _make_runner(schedule, past_time="00:00"):
    """Create a runner with a mock say function.
    All event times set to past so they fire immediately."""
    said = []
    runner = Runner(schedule, say_fn=lambda t: said.append(t))
    return runner, said


def test_fire_all_events_immediately():
    """When all events are in the past, they fire sequentially."""
    schedule = [
        {"type": "task_start", "task": "study", "time": "00:01"},
        {"type": "break", "time": "00:02"},
        {"type": "resume", "task": "study", "time": "00:03"},
        {"type": "task_end", "task": "study", "time": "00:04"},
    ]
    runner, said = _make_runner(schedule)
    runner.start()

    # task_start + break + resume + task_end + day_end
    assert len(said) == 5
    assert "study" in said[0].lower() or "study" in said[0]
    assert "break" in said[1].lower()
    assert "study" in said[2].lower()
    assert "study" in said[3].lower()
    assert "done" in said[4].lower() or "everything" in said[4].lower()


def test_fire_multi_task():
    """Two tasks back to back."""
    schedule = [
        {"type": "task_start", "task": "emails", "time": "00:01"},
        {"type": "task_end", "task": "emails", "time": "00:02"},
        {"type": "task_start", "task": "study", "time": "00:02"},
        {"type": "task_end", "task": "study", "time": "00:03"},
    ]
    runner, said = _make_runner(schedule)
    runner.start()

    # task_start(emails) + task_end(emails, next=study) + task_start(study) + task_end(study) + day_end
    assert len(said) == 5
    assert "study" in said[1]  # task_end mentions next task


# --- handle_action ---


def test_action_stop():
    schedule = _sample_schedule()
    runner, said = _make_runner(schedule)
    runner._event_idx = 1  # pretend we're mid-schedule
    runner.handle_action({"action": "stop"})
    assert runner._stop_event.is_set()
    assert any("stop" in s.lower() for s in said)


def test_action_skip_break():
    schedule = [
        {"type": "task_start", "task": "study", "time": "00:01"},
        {"type": "break", "time": "00:02"},
        {"type": "resume", "task": "study", "time": "00:03"},
        {"type": "task_end", "task": "study", "time": "00:04"},
    ]
    runner, said = _make_runner(schedule)
    runner._event_idx = 1  # at the break
    runner.handle_action({"action": "skip_break"})
    # Should skip break + resume, land on task_end
    assert runner._event_idx >= 3  # at or past task_end


def test_action_finish_early():
    schedule = [
        {"type": "task_start", "task": "study", "time": "00:01"},
        {"type": "break", "time": "00:02"},
        {"type": "resume", "task": "study", "time": "00:03"},
        {"type": "task_end", "task": "study", "time": "00:04"},
        {"type": "task_start", "task": "code", "time": "00:04"},
        {"type": "task_end", "task": "code", "time": "00:05"},
    ]
    runner, said = _make_runner(schedule)
    runner._event_idx = 1  # at break during study
    runner.handle_action({"action": "finish_early"})
    # Should jump to next task_start (code)
    assert any("code" in s.lower() for s in said)


def test_action_extend():
    schedule = [
        {"type": "task_end", "task": "study", "time": "00:30"},
        {"type": "task_start", "task": "code", "time": "00:30"},
        {"type": "task_end", "task": "code", "time": "01:00"},
    ]
    runner, said = _make_runner(schedule)
    runner._event_idx = 0
    runner.handle_action({"action": "extend", "minutes": 10})
    # All remaining events shifted by 10 min
    assert schedule[0]["time"] == "00:40"
    assert schedule[1]["time"] == "00:40"
    assert schedule[2]["time"] == "01:10"


def test_action_status():
    schedule = _sample_schedule()
    runner, said = _make_runner(schedule)
    runner._event_idx = 2
    runner.handle_action({"action": "status"})
    assert len(said) == 1
    assert "resume" in said[0].lower() or "09:25" in said[0]


# --- save/load state ---


def test_save_and_load(tmp_path, monkeypatch):
    import runner as runner_mod
    monkeypatch.setattr(runner_mod, "STATE_DIR", tmp_path)

    schedule = _sample_schedule()
    r, _ = _make_runner(schedule)
    r._event_idx = 2
    r._save_state()

    loaded = Runner.load(say_fn=lambda t: None)
    assert loaded is not None
    assert loaded._event_idx == 2
    assert loaded.schedule == schedule
