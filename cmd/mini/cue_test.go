package main

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func testStyles() map[string]CueWorkStyle {
	return map[string]CueWorkStyle{
		"learning": {BreakEveryMin: 20, BreakDurationMin: 5},
		"admin":    {BreakEveryMin: 0, BreakDurationMin: 0},
		"deep":     {BreakEveryMin: 45, BreakDurationMin: 10},
	}
}

func testState(t *testing.T) *cueState {
	t.Helper()
	dir := t.TempDir()
	cc := defaultCueConfig()
	timerCh := make(chan string, 10)
	var pendingTimer *time.Timer
	var mu sync.Mutex
	return &cueState{
		config:       &cc,
		plan:         newCuePlan(),
		sessDir:      dir,
		timerCh:      timerCh,
		pendingMu:    &mu,
		pendingTimer: &pendingTimer,
	}
}

// ── Timeline tests ───────────────────────────────────────

func TestCueTimelineLearning60(t *testing.T) {
	tl := cueCalculateTimeline(
		[]CueTask{{Name: "study", DurationMin: 60, WorkStyle: "learning"}},
		testStyles(), "09:00", "",
	)
	assertEqual(t, "total_work", tl.TotalWorkMin, 60)
	assertEqual(t, "total_break", tl.TotalBreakMin, 10)
	assertEqual(t, "total", tl.TotalMin, 70)
	assertEqual(t, "end_time", tl.EndTime, "10:10")

	assertEvent(t, tl.Timeline, 0, "task_start", "study", "09:00")
	assertEvent(t, tl.Timeline, 1, "break", "", "09:20")
	assertEvent(t, tl.Timeline, 2, "resume", "study", "09:25")
	assertEvent(t, tl.Timeline, 3, "break", "", "09:45")
	assertEvent(t, tl.Timeline, 4, "resume", "study", "09:50")
	assertEvent(t, tl.Timeline, 5, "task_end", "study", "10:10")
}

func TestCueTimelineAdminNoBreaks(t *testing.T) {
	tl := cueCalculateTimeline(
		[]CueTask{{Name: "emails", DurationMin: 30, WorkStyle: "admin"}},
		testStyles(), "10:00", "",
	)
	assertEqual(t, "total_work", tl.TotalWorkMin, 30)
	assertEqual(t, "total_break", tl.TotalBreakMin, 0)
	assertEqual(t, "end_time", tl.EndTime, "10:30")
	assertEqual(t, "events", len(tl.Timeline), 2)
}

func TestCueTimelineMultipleTasks(t *testing.T) {
	tl := cueCalculateTimeline(
		[]CueTask{
			{Name: "emails", DurationMin: 30, WorkStyle: "admin"},
			{Name: "study", DurationMin: 40, WorkStyle: "learning"},
		},
		testStyles(), "09:00", "",
	)
	assertEqual(t, "total_work", tl.TotalWorkMin, 70)
	assertEqual(t, "total_break", tl.TotalBreakMin, 5)
	assertEqual(t, "total", tl.TotalMin, 75)
	assertEqual(t, "end_time", tl.EndTime, "10:15")
}

func TestCueTimelineEndTimeFits(t *testing.T) {
	tl := cueCalculateTimeline(
		[]CueTask{{Name: "emails", DurationMin: 30, WorkStyle: "admin"}},
		testStyles(), "10:00", "11:00",
	)
	assertEqual(t, "fits", tl.Fits, true)
	assertEqual(t, "overflow", tl.OverflowMin, 0)
}

func TestCueTimelineEndTimeOverflows(t *testing.T) {
	tl := cueCalculateTimeline(
		[]CueTask{{Name: "study", DurationMin: 60, WorkStyle: "learning"}},
		testStyles(), "09:00", "10:00",
	)
	assertEqual(t, "fits", tl.Fits, false)
	assertEqual(t, "overflow", tl.OverflowMin, 10)
}

func TestCueTimelineShorterThanBreak(t *testing.T) {
	tl := cueCalculateTimeline(
		[]CueTask{{Name: "quick", DurationMin: 15, WorkStyle: "learning"}},
		testStyles(), "14:00", "",
	)
	assertEqual(t, "total_work", tl.TotalWorkMin, 15)
	assertEqual(t, "total_break", tl.TotalBreakMin, 0)
	assertEqual(t, "events", len(tl.Timeline), 2)
}

// ── Manage styles tests ──────────────────────────────────

func TestCueManageStylesList(t *testing.T) {
	cc := defaultCueConfig()
	result, _ := cueToolManageStyles(map[string]any{"action": "list"}, &cc)
	m := result.(map[string]any)
	if m["styles"] == nil {
		t.Fatal("expected styles in result")
	}
}

func TestCueManageStylesCreate(t *testing.T) {
	cc := defaultCueConfig()
	result, _ := cueToolManageStyles(map[string]any{
		"action": "create",
		"name":   "pomodoro",
		"style":  map[string]any{"break_every_min": 25, "break_duration_min": 5},
	}, &cc)
	m := result.(map[string]any)
	assertEqual(t, "ok", m["ok"], true)
	if _, exists := cc.WorkStyles["pomodoro"]; !exists {
		t.Fatal("pomodoro style should exist after create")
	}
}

func TestCueManageStylesCreateDuplicate(t *testing.T) {
	cc := defaultCueConfig()
	result, _ := cueToolManageStyles(map[string]any{
		"action": "create",
		"name":   "learning",
		"style":  map[string]any{"break_every_min": 30},
	}, &cc)
	m := result.(map[string]any)
	assertEqual(t, "ok", m["ok"], false)
}

func TestCueManageStylesUpdate(t *testing.T) {
	cc := defaultCueConfig()
	cueToolManageStyles(map[string]any{
		"action": "update",
		"name":   "learning",
		"style":  map[string]any{"break_every_min": 30},
	}, &cc)
	assertEqual(t, "updated", cc.WorkStyles["learning"].BreakEveryMin, 30)
	// Original field preserved
	assertEqual(t, "preserved", cc.WorkStyles["learning"].BreakDurationMin, 5)
}

func TestCueManageStylesDeleteMissing(t *testing.T) {
	cc := defaultCueConfig()
	result, _ := cueToolManageStyles(map[string]any{
		"action": "delete",
		"name":   "nope",
	}, &cc)
	m := result.(map[string]any)
	assertEqual(t, "ok", m["ok"], false)
}

// ── System prompt ────────────────────────────────────────

func TestCueSystemPromptNoPlan(t *testing.T) {
	cc := defaultCueConfig()
	plan := newCuePlan()
	prompt := buildCueSystem(cc, plan, t.TempDir())
	if !containsStr(prompt, "Current time:") {
		t.Error("prompt should contain current time")
	}
	if !containsStr(prompt, "set_plan") {
		t.Error("prompt should mention set_plan tool")
	}
	if !containsStr(prompt, "advance()") {
		t.Error("prompt should mention advance tool")
	}
	if !containsStr(prompt, "call set_plan immediately") {
		t.Error("prompt should instruct immediate set_plan usage")
	}
	if containsStr(prompt, "## Plan") {
		t.Error("prompt should NOT contain plan section when plan is empty")
	}
}

func TestCueSystemPromptWithPlan(t *testing.T) {
	cc := defaultCueConfig()
	plan := &CuePlan{
		Items: []CuePlanItem{
			{Name: "study", DurationMin: 60, WorkStyle: "learning", Status: "done"},
			{Name: "music", DurationMin: 120, WorkStyle: "exploration", Status: "active", StartedAt: "10:00"},
			{Name: "painting", DurationMin: 60, WorkStyle: "exploration", Status: "pending"},
		},
		ActiveIdx: 1,
	}
	prompt := buildCueSystem(cc, plan, t.TempDir())
	if !containsStr(prompt, "## Plan") {
		t.Error("prompt should contain plan section")
	}
	if !containsStr(prompt, "[done] study") {
		t.Error("prompt should show study as done")
	}
	if !containsStr(prompt, "[active] music") {
		t.Error("prompt should show music as active")
	}
	if !containsStr(prompt, "[pending] painting") {
		t.Error("prompt should show painting as pending")
	}
}

func TestCueSystemPromptWithPendingTimer(t *testing.T) {
	cc := defaultCueConfig()
	plan := &CuePlan{
		Items: []CuePlanItem{
			{Name: "coding", DurationMin: 60, WorkStyle: "deep", Status: "active", StartedAt: "16:00"},
		},
		ActiveIdx: 0,
	}
	dir := t.TempDir()
	saveCuePending(dir, &CuePending{NextTime: "16:25", NextLabel: "[TIMER] break", EventType: "break"})
	prompt := buildCueSystem(cc, plan, dir)
	if !containsStr(prompt, "Next timer: break at 16:25") {
		t.Error("prompt should show pending timer")
	}
	if !containsStr(prompt, "Do NOT announce it early") {
		t.Error("prompt should warn against early announcement")
	}
	saveCuePending(dir, nil)
}

// ── Plan persistence ─────────────────────────────────────

func TestCuePlanRoundtrip(t *testing.T) {
	dir := t.TempDir()
	plan := &CuePlan{
		Items: []CuePlanItem{
			{Name: "coding", DurationMin: 60, WorkStyle: "deep", Status: "active", StartedAt: "09:00"},
			{Name: "email", DurationMin: 30, WorkStyle: "admin", Status: "pending"},
		},
		ActiveIdx: 0,
	}
	saveCuePlan(dir, plan)
	loaded := loadCuePlan(dir)
	assertEqual(t, "items_len", len(loaded.Items), 2)
	assertEqual(t, "active_idx", loaded.ActiveIdx, 0)
	assertEqual(t, "item0_name", loaded.Items[0].Name, "coding")
	assertEqual(t, "item0_status", loaded.Items[0].Status, "active")
	assertEqual(t, "item1_status", loaded.Items[1].Status, "pending")
}

func TestCuePlanLoadMissing(t *testing.T) {
	dir := t.TempDir()
	plan := loadCuePlan(dir)
	assertEqual(t, "active_idx", plan.ActiveIdx, -1)
	assertEqual(t, "items_len", len(plan.Items), 0)
}

// ── Pending state ────────────────────────────────────────

func TestCuePendingRoundtrip(t *testing.T) {
	dir := t.TempDir()
	p := &CuePending{NextTime: "15:30", NextLabel: "[TIMER] break", EventType: "break"}
	saveCuePending(dir, p)
	loaded := loadCuePending(dir)
	if loaded == nil {
		t.Fatal("expected non-nil pending")
	}
	assertEqual(t, "time", loaded.NextTime, "15:30")
	assertEqual(t, "label", loaded.NextLabel, "[TIMER] break")
	assertEqual(t, "event_type", loaded.EventType, "break")
	// Clean up
	saveCuePending(dir, nil)
	if loadCuePending(dir) != nil {
		t.Error("expected nil after clear")
	}
}

// ── set_plan tool ────────────────────────────────────────

func TestCueToolSetPlan(t *testing.T) {
	st := testState(t)
	result, err := cueRunTool("set_plan", map[string]any{
		"items": []any{
			map[string]any{"name": "coding", "duration_min": float64(60), "work_style": "deep"},
			map[string]any{"name": "email", "duration_min": float64(30), "work_style": "admin"},
		},
		"start_time": "09:00",
	}, st)
	if err != nil {
		t.Fatal(err)
	}
	m := result.(map[string]any)
	assertEqual(t, "ok", m["ok"], true)
	assertEqual(t, "items", m["items"], 2)
	assertEqual(t, "active", m["active"], "coding")

	// Check plan state
	assertEqual(t, "plan_items", len(st.plan.Items), 2)
	assertEqual(t, "plan_active_idx", st.plan.ActiveIdx, 0)
	assertEqual(t, "item0_status", st.plan.Items[0].Status, "active")
	assertEqual(t, "item1_status", st.plan.Items[1].Status, "pending")

	// Check persistence
	loaded := loadCuePlan(st.sessDir)
	assertEqual(t, "persisted_items", len(loaded.Items), 2)
	assertEqual(t, "persisted_active", loaded.ActiveIdx, 0)

	// Clean up timer
	cueStopTimer(st)
}

func TestCueToolSetPlanResolvesOverrides(t *testing.T) {
	st := testState(t)
	result, err := cueRunTool("set_plan", map[string]any{
		"items": []any{
			map[string]any{"name": "linux skills", "duration_min": float64(60)},
		},
		"start_time": "09:00",
	}, st)
	if err != nil {
		t.Fatal(err)
	}
	m := result.(map[string]any)
	assertEqual(t, "ok", m["ok"], true)
	// "linux skills" should resolve to "learning" via task_overrides
	assertEqual(t, "work_style", st.plan.Items[0].WorkStyle, "learning")
	cueStopTimer(st)
}

func TestCueToolSetPlanEmpty(t *testing.T) {
	st := testState(t)
	result, _ := cueRunTool("set_plan", map[string]any{
		"items":      []any{},
		"start_time": "09:00",
	}, st)
	m := result.(map[string]any)
	assertEqual(t, "ok", m["ok"], false)
}

// ── advance tool ─────────────────────────────────────────

func TestCueToolAdvance(t *testing.T) {
	st := testState(t)

	// Set up a plan with 2 items, first active
	st.plan.Items = []CuePlanItem{
		{Name: "coding", DurationMin: 60, WorkStyle: "deep", Status: "active", StartedAt: "09:00"},
		{Name: "email", DurationMin: 30, WorkStyle: "admin", Status: "pending"},
	}
	st.plan.ActiveIdx = 0

	result, err := cueRunTool("advance", map[string]any{}, st)
	if err != nil {
		t.Fatal(err)
	}
	m := result.(map[string]any)
	assertEqual(t, "ok", m["ok"], true)
	assertEqual(t, "active", m["active"], "email")

	// Check state
	assertEqual(t, "item0_status", st.plan.Items[0].Status, "done")
	assertEqual(t, "item1_status", st.plan.Items[1].Status, "active")
	assertEqual(t, "active_idx", st.plan.ActiveIdx, 1)

	cueStopTimer(st)
}

func TestCueToolAdvanceLastTask(t *testing.T) {
	st := testState(t)

	st.plan.Items = []CuePlanItem{
		{Name: "coding", DurationMin: 60, WorkStyle: "deep", Status: "done", StartedAt: "09:00"},
		{Name: "email", DurationMin: 30, WorkStyle: "admin", Status: "active", StartedAt: "10:00"},
	}
	st.plan.ActiveIdx = 1

	result, err := cueRunTool("advance", map[string]any{}, st)
	if err != nil {
		t.Fatal(err)
	}
	m := result.(map[string]any)
	assertEqual(t, "ok", m["ok"], true)
	assertEqual(t, "message", m["message"], "all tasks done")
	assertEqual(t, "active_idx", st.plan.ActiveIdx, -1)
}

func TestCueToolAdvanceNoActive(t *testing.T) {
	st := testState(t)
	result, _ := cueRunTool("advance", map[string]any{}, st)
	m := result.(map[string]any)
	assertEqual(t, "ok", m["ok"], false)
}

// ── Tool dispatch ────────────────────────────────────────

func TestCueToolCurrentTime(t *testing.T) {
	st := testState(t)
	result, err := cueRunTool("current_time", map[string]any{}, st)
	if err != nil {
		t.Fatal(err)
	}
	m := result.(map[string]string)
	if m["now"] == "" {
		t.Error("expected non-empty time")
	}
}

func TestCueToolUnknown(t *testing.T) {
	st := testState(t)
	result, _ := cueRunTool("bogus", map[string]any{}, st)
	m := result.(map[string]string)
	if m["error"] == "" {
		t.Error("expected error for unknown tool")
	}
}

func TestCueToolDefs(t *testing.T) {
	names := map[string]bool{}
	for _, td := range cueToolDefs {
		names[td.Function.Name] = true
	}
	for _, expected := range []string{"say", "set_plan", "advance", "calculate_timeline", "manage_work_styles", "current_time"} {
		if !names[expected] {
			t.Errorf("missing tool def: %s", expected)
		}
	}
	// schedule should NOT be present
	if names["schedule"] {
		t.Error("schedule tool should have been removed")
	}
}

// ── Timer scheduling ─────────────────────────────────────

func TestCueActivateItemSchedulesTimer(t *testing.T) {
	st := testState(t)
	st.plan.Items = []CuePlanItem{
		{Name: "study", DurationMin: 60, WorkStyle: "learning", Status: "pending"},
	}

	cueActivateItem(st, 0, time.Now().Add(1*time.Minute).Format("15:04"))

	assertEqual(t, "status", st.plan.Items[0].Status, "active")

	// Should have a pending timer
	pending := loadCuePending(st.sessDir)
	if pending == nil {
		t.Fatal("expected pending timer after activation")
	}
	assertEqual(t, "event_type", pending.EventType, "break")

	cueStopTimer(st)
}

func TestCueActivateAdminNoBreaks(t *testing.T) {
	st := testState(t)
	st.plan.Items = []CuePlanItem{
		{Name: "emails", DurationMin: 30, WorkStyle: "admin", Status: "pending"},
	}

	cueActivateItem(st, 0, time.Now().Add(1*time.Minute).Format("15:04"))

	// Admin style: should schedule task_end, not break
	pending := loadCuePending(st.sessDir)
	if pending == nil {
		t.Fatal("expected pending timer")
	}
	assertEqual(t, "event_type", pending.EventType, "task_end")

	cueStopTimer(st)
}

// ── Pending persistence (session-scoped) ─────────────────

func TestCuePendingSessionScoped(t *testing.T) {
	dir1 := t.TempDir()
	dir2 := t.TempDir()

	saveCuePending(dir1, &CuePending{NextTime: "10:00", NextLabel: "break", EventType: "break"})
	saveCuePending(dir2, &CuePending{NextTime: "11:00", NextLabel: "resume", EventType: "resume"})

	p1 := loadCuePending(dir1)
	p2 := loadCuePending(dir2)

	assertEqual(t, "dir1_time", p1.NextTime, "10:00")
	assertEqual(t, "dir2_time", p2.NextTime, "11:00")

	// Clean up
	saveCuePending(dir1, nil)
	saveCuePending(dir2, nil)
}

// ── Plan file location ───────────────────────────────────

func TestCuePlanFileLocation(t *testing.T) {
	dir := t.TempDir()
	plan := &CuePlan{
		Items:     []CuePlanItem{{Name: "test", DurationMin: 10, WorkStyle: "admin", Status: "pending"}},
		ActiveIdx: -1,
	}
	saveCuePlan(dir, plan)

	// Verify plan.json exists in session dir
	_, err := os.Stat(filepath.Join(dir, "plan.json"))
	if err != nil {
		t.Fatalf("plan.json should exist in session dir: %v", err)
	}
}

// ── cuePlanSummary ───────────────────────────────────────

func TestCuePlanSummary(t *testing.T) {
	plan := &CuePlan{
		Items: []CuePlanItem{
			{Name: "coding", DurationMin: 60, WorkStyle: "deep", Status: "done", StartedAt: "09:00"},
			{Name: "email", DurationMin: 30, WorkStyle: "admin", Status: "active", StartedAt: "10:00"},
		},
		ActiveIdx: 1,
	}
	summary := cuePlanSummary(plan)
	assertEqual(t, "len", len(summary), 2)
	assertEqual(t, "item0_name", summary[0]["name"], "coding")
	assertEqual(t, "item0_status", summary[0]["status"], "done")
	assertEqual(t, "item1_name", summary[1]["name"], "email")
	assertEqual(t, "item1_status", summary[1]["status"], "active")
}

// ── roundUp5 ─────────────────────────────────────────────

func TestCueRoundUp5(t *testing.T) {
	assertEqual(t, "0", roundUp5(0), 0)
	assertEqual(t, "5", roundUp5(5), 5)
	assertEqual(t, "1->5", roundUp5(1), 5)
	assertEqual(t, "9->10", roundUp5(9), 10)
	assertEqual(t, "10", roundUp5(10), 10)
	assertEqual(t, "11->15", roundUp5(11), 15)
	assertEqual(t, "59->60", roundUp5(59), 60)
}

func TestCueRoundTimeUp5(t *testing.T) {
	assertEqual(t, "16:09->16:10", roundTimeUp5("16:09"), "16:10")
	assertEqual(t, "16:10->16:10", roundTimeUp5("16:10"), "16:10")
	assertEqual(t, "16:11->16:15", roundTimeUp5("16:11"), "16:15")
	assertEqual(t, "09:01->09:05", roundTimeUp5("09:01"), "09:05")
}

// ── parseHHMM / fmtHHMM ─────────────────────────────────

func TestCueParseHHMM(t *testing.T) {
	assertEqual(t, "09:00", parseHHMM("09:00"), 540)
	assertEqual(t, "13:30", parseHHMM("13:30"), 810)
}

func TestCueFmtHHMM(t *testing.T) {
	assertEqual(t, "540", fmtHHMM(540), "09:00")
	assertEqual(t, "810", fmtHHMM(810), "13:30")
}

// ── helpers ──────────────────────────────────────────────

func assertEqual[T comparable](t *testing.T, label string, got, want T) {
	t.Helper()
	if got != want {
		t.Errorf("%s: got %v, want %v", label, got, want)
	}
}

func assertEvent(t *testing.T, events []CueEvent, idx int, etype, task, time string) {
	t.Helper()
	if idx >= len(events) {
		t.Fatalf("event[%d]: index out of range (len=%d)", idx, len(events))
	}
	e := events[idx]
	if e.Type != etype {
		t.Errorf("event[%d].type: got %q, want %q", idx, e.Type, etype)
	}
	if task != "" && e.Task != task {
		t.Errorf("event[%d].task: got %q, want %q", idx, e.Task, task)
	}
	if e.Time != time {
		t.Errorf("event[%d].time: got %q, want %q", idx, e.Time, time)
	}
}

func containsStr(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(s) > 0 && contains(s, sub))
}

func contains(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
