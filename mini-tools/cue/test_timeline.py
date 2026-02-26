from timeline import calculate_timeline

STYLES = {
    "learning": {"break_every_min": 20, "break_duration_min": 5},
    "admin": {"break_every_min": 0, "break_duration_min": 0},
    "deep": {"break_every_min": 45, "break_duration_min": 10},
}


def test_learning_60min():
    """60 min learning: 3 work blocks (20+20+20), 2 breaks (5+5) = 70 min."""
    result = calculate_timeline(
        [{"name": "study", "duration_min": 60, "work_style": "learning"}],
        STYLES, "09:00",
    )
    assert result["total_work_min"] == 60
    assert result["total_break_min"] == 10
    assert result["total_min"] == 70
    assert result["end_time"] == "10:10"

    tl = result["timeline"]
    assert tl[0] == {"type": "task_start", "task": "study", "time": "09:00"}
    assert tl[1] == {"type": "break", "time": "09:20"}
    assert tl[2] == {"type": "resume", "task": "study", "time": "09:25"}
    assert tl[3] == {"type": "break", "time": "09:45"}
    assert tl[4] == {"type": "resume", "task": "study", "time": "09:50"}
    assert tl[5] == {"type": "task_end", "task": "study", "time": "10:10"}


def test_admin_no_breaks():
    """Admin style: no breaks, just start and end."""
    result = calculate_timeline(
        [{"name": "emails", "duration_min": 30, "work_style": "admin"}],
        STYLES, "10:00",
    )
    assert result["total_work_min"] == 30
    assert result["total_break_min"] == 0
    assert result["total_min"] == 30
    assert result["end_time"] == "10:30"

    tl = result["timeline"]
    assert len(tl) == 2
    assert tl[0] == {"type": "task_start", "task": "emails", "time": "10:00"}
    assert tl[1] == {"type": "task_end", "task": "emails", "time": "10:30"}


def test_multiple_tasks():
    """Back-to-back tasks with different styles."""
    tasks = [
        {"name": "emails", "duration_min": 30, "work_style": "admin"},
        {"name": "study", "duration_min": 40, "work_style": "learning"},
    ]
    result = calculate_timeline(tasks, STYLES, "09:00")

    assert result["total_work_min"] == 70
    # study: 40 min learning (20 on / 5 off) → 20 + break + 20, one break
    assert result["total_break_min"] == 5
    assert result["total_min"] == 75
    assert result["end_time"] == "10:15"

    tl = result["timeline"]
    # emails block
    assert tl[0] == {"type": "task_start", "task": "emails", "time": "09:00"}
    assert tl[1] == {"type": "task_end", "task": "emails", "time": "09:30"}
    # study block
    assert tl[2] == {"type": "task_start", "task": "study", "time": "09:30"}
    assert tl[3] == {"type": "break", "time": "09:50"}
    assert tl[4] == {"type": "resume", "task": "study", "time": "09:55"}
    assert tl[5] == {"type": "task_end", "task": "study", "time": "10:15"}


def test_end_time_fits():
    """End time constraint that fits."""
    result = calculate_timeline(
        [{"name": "emails", "duration_min": 30, "work_style": "admin"}],
        STYLES, "10:00", end_time="11:00",
    )
    assert result["fits"] is True
    assert result["overflow_min"] == 0


def test_end_time_overflows():
    """End time constraint that overflows."""
    result = calculate_timeline(
        [{"name": "study", "duration_min": 60, "work_style": "learning"}],
        STYLES, "09:00", end_time="10:00",
    )
    assert result["fits"] is False
    assert result["overflow_min"] == 10  # ends at 10:10, window ends 10:00


def test_task_shorter_than_break_interval():
    """Task shorter than break_every_min → no breaks."""
    result = calculate_timeline(
        [{"name": "quick", "duration_min": 15, "work_style": "learning"}],
        STYLES, "14:00",
    )
    assert result["total_work_min"] == 15
    assert result["total_break_min"] == 0
    assert result["total_min"] == 15

    tl = result["timeline"]
    assert len(tl) == 2
    assert tl[0] == {"type": "task_start", "task": "quick", "time": "14:00"}
    assert tl[1] == {"type": "task_end", "task": "quick", "time": "14:15"}
