def calculate_timeline(tasks, work_styles, start_time, end_time=None):
    """Expand tasks into a flat event list with breaks inserted.

    Args:
        tasks: list of {"name", "duration_min", "work_style"}
        work_styles: dict of style name -> {"break_every_min", "break_duration_min"}
        start_time: "HH:MM" string
        end_time: optional "HH:MM" constraint

    Returns:
        dict with timeline, total_work_min, total_break_min, total_min,
        end_time, fits, overflow_min
    """
    cursor = _parse_time(start_time)
    events = []
    total_work = 0
    total_break = 0

    for task in tasks:
        style = work_styles[task["work_style"]]
        break_every = style["break_every_min"]
        break_dur = style["break_duration_min"]
        remaining = task["duration_min"]

        events.append({"type": "task_start", "task": task["name"], "time": _fmt(cursor)})

        if break_every <= 0:
            # No breaks (e.g. admin style)
            cursor += remaining
            total_work += remaining
        else:
            while remaining > 0:
                chunk = min(remaining, break_every)
                cursor += chunk
                total_work += chunk
                remaining -= chunk
                if remaining > 0:
                    events.append({"type": "break", "time": _fmt(cursor)})
                    cursor += break_dur
                    total_break += break_dur
                    events.append({"type": "resume", "task": task["name"], "time": _fmt(cursor)})

        events.append({"type": "task_end", "task": task["name"], "time": _fmt(cursor)})

    total_min = total_work + total_break
    computed_end = _fmt(cursor)

    fits = True
    overflow = 0
    if end_time is not None:
        end_minutes = _parse_time(end_time)
        if cursor > end_minutes:
            fits = False
            overflow = cursor - end_minutes

    return {
        "timeline": events,
        "total_work_min": total_work,
        "total_break_min": total_break,
        "total_min": total_min,
        "end_time": computed_end,
        "fits": fits,
        "overflow_min": overflow,
    }


def _parse_time(s):
    """Parse 'HH:MM' to minutes since midnight."""
    h, m = s.split(":")
    return int(h) * 60 + int(m)


def _fmt(minutes):
    """Format minutes since midnight as 'HH:MM'."""
    return f"{minutes // 60:02d}:{minutes % 60:02d}"
