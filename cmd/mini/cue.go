package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

// ── Cue types ────────────────────────────────────────────

type CueConfig struct {
	Model         string                  `yaml:"model"`
	DefaultStart  string                  `yaml:"default_start"`
	DefaultEnd    string                  `yaml:"default_end"`
	WorkStyles    map[string]CueWorkStyle `yaml:"work_styles"`
	TaskOverrides map[string]string       `yaml:"task_overrides"`
}

type CueWorkStyle struct {
	Description      string   `yaml:"description"`
	BreakEveryMin    int      `yaml:"break_every_min"`
	BreakDurationMin int      `yaml:"break_duration_min"`
	Examples         []string `yaml:"examples"`
}

type CueTask struct {
	Name        string `json:"name"`
	DurationMin int    `json:"duration_min"`
	WorkStyle   string `json:"work_style,omitempty"`
}

type CueEvent struct {
	Type string `json:"type"`
	Task string `json:"task,omitempty"`
	Time string `json:"time"`
}

type CueTimeline struct {
	Timeline      []CueEvent `json:"timeline"`
	TotalWorkMin  int        `json:"total_work_min"`
	TotalBreakMin int        `json:"total_break_min"`
	TotalMin      int        `json:"total_min"`
	EndTime       string     `json:"end_time"`
	Fits          bool       `json:"fits"`
	OverflowMin   int        `json:"overflow_min"`
	CurrentTime   string     `json:"current_time"`
	Warning       string     `json:"warning,omitempty"`
}

// ── Plan state (Go-owned) ────────────────────────────────

type CuePlanItem struct {
	Name        string `json:"name"`
	DurationMin int    `json:"duration_min"`
	WorkStyle   string `json:"work_style"`
	Status      string `json:"status"`     // pending, active, done, skipped
	StartedAt   string `json:"started_at"` // HH:MM or empty
}

type CuePlan struct {
	Items     []CuePlanItem `json:"items"`
	ActiveIdx int           `json:"active_idx"` // -1 = none active
}

func newCuePlan() *CuePlan {
	return &CuePlan{ActiveIdx: -1}
}

// CuePending is the state file for timer persistence across restarts.
type CuePending struct {
	NextTime  string `json:"next_time"`
	NextLabel string `json:"next_label"`
	EventType string `json:"event_type"` // break, resume, task_end
}

// ── Config ───────────────────────────────────────────────

func defaultCueConfig() CueConfig {
	return CueConfig{
		Model:        "qwen3-coder:30b",
		DefaultStart: "09:00",
		DefaultEnd:   "18:00",
		WorkStyles: map[string]CueWorkStyle{
			"deep": {
				Description:      "Deep technical work requiring sustained focus",
				BreakEveryMin:    25,
				BreakDurationMin: 5,
				Examples:         []string{"coding", "debugging", "refactoring", "system design"},
			},
			"learning": {
				Description:      "Studying, reading docs, following tutorials",
				BreakEveryMin:    20,
				BreakDurationMin: 5,
				Examples:         []string{"reading docs", "tutorial", "course", "studying"},
			},
			"exploration": {
				Description:      "Exploratory work: experimenting, tinkering, setup",
				BreakEveryMin:    30,
				BreakDurationMin: 5,
				Examples:         []string{"experimenting", "setting up", "prototyping"},
			},
			"admin": {
				Description:      "Low-focus admin: emails, PRs, issues, updates",
				BreakEveryMin:    0,
				BreakDurationMin: 0,
				Examples:         []string{"email", "PR review", "issues", "updates"},
			},
		},
		TaskOverrides: map[string]string{
			"linux skills": "learning",
			"mini-llm":     "deep",
			"web3 project": "deep",
		},
	}
}

func loadCueConfig() CueConfig {
	cc := defaultCueConfig()

	home, err := os.UserHomeDir()
	if err != nil {
		return cc
	}
	data, err := os.ReadFile(filepath.Join(home, ".mini", "cue.yaml"))
	if err != nil {
		return cc
	}
	if err := yaml.Unmarshal(data, &cc); err != nil {
		return defaultCueConfig()
	}
	if cc.WorkStyles == nil {
		cc.WorkStyles = defaultCueConfig().WorkStyles
	}
	return cc
}

func saveCueConfig(cc CueConfig) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	dir := filepath.Join(home, ".mini")
	os.MkdirAll(dir, 0755)
	data, err := yaml.Marshal(cc)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "cue.yaml"), data, 0644)
}

// ── Plan persistence ─────────────────────────────────────

func loadCuePlan(sessDir string) *CuePlan {
	data, err := os.ReadFile(filepath.Join(sessDir, "plan.json"))
	if err != nil {
		return newCuePlan()
	}
	var p CuePlan
	if json.Unmarshal(data, &p) != nil {
		return newCuePlan()
	}
	return &p
}

func saveCuePlan(sessDir string, plan *CuePlan) {
	data, _ := json.MarshalIndent(plan, "", "  ")
	os.WriteFile(filepath.Join(sessDir, "plan.json"), data, 0644)
}

// ── Pending state (timer persistence) ────────────────────

func cuePendingPath(sessDir string) string {
	return filepath.Join(sessDir, "pending.json")
}

func loadCuePending(sessDir string) *CuePending {
	data, err := os.ReadFile(cuePendingPath(sessDir))
	if err != nil {
		return nil
	}
	var p CuePending
	if json.Unmarshal(data, &p) != nil || p.NextTime == "" {
		return nil
	}
	return &p
}

func saveCuePending(sessDir string, p *CuePending) {
	path := cuePendingPath(sessDir)
	if p == nil {
		os.Remove(path)
		return
	}
	data, _ := json.Marshal(p)
	os.WriteFile(path, data, 0644)
}

// ── Timeline ─────────────────────────────────────────────

func cueCalculateTimeline(tasks []CueTask, styles map[string]CueWorkStyle, startTime string, endTime string) CueTimeline {
	cursor := parseHHMM(startTime)
	var events []CueEvent
	totalWork := 0
	totalBreak := 0

	for i, task := range tasks {
		style := styles[task.WorkStyle]
		breakEvery := style.BreakEveryMin
		breakDur := style.BreakDurationMin
		remaining := task.DurationMin

		events = append(events, CueEvent{Type: "task_start", Task: task.Name, Time: fmtHHMM(cursor)})

		if breakEvery <= 0 {
			cursor += remaining
			totalWork += remaining
		} else {
			for remaining > 0 {
				chunk := remaining
				if chunk > breakEvery {
					chunk = breakEvery
				}
				cursor += chunk
				totalWork += chunk
				remaining -= chunk
				if remaining > 0 {
					events = append(events, CueEvent{Type: "break", Time: fmtHHMM(cursor)})
					cursor += breakDur
					totalBreak += breakDur
					events = append(events, CueEvent{Type: "resume", Task: task.Name, Time: fmtHHMM(cursor)})
				}
			}
		}

		events = append(events, CueEvent{Type: "task_end", Task: task.Name, Time: fmtHHMM(cursor)})
		_ = i
	}

	tl := CueTimeline{
		Timeline:      events,
		TotalWorkMin:  totalWork,
		TotalBreakMin: totalBreak,
		TotalMin:      totalWork + totalBreak,
		EndTime:       fmtHHMM(cursor),
		Fits:          true,
		OverflowMin:   0,
	}

	if endTime != "" {
		endMin := parseHHMM(endTime)
		if cursor > endMin {
			tl.Fits = false
			tl.OverflowMin = cursor - endMin
		}
	}

	nowTime := time.Now()
	tl.CurrentTime = nowTime.Format("15:04")
	nowMin := nowTime.Hour()*60 + nowTime.Minute()
	if parseHHMM(startTime) < nowMin {
		tl.Warning = fmt.Sprintf("start_time %s is in the past (current time is %s)", startTime, tl.CurrentTime)
	}

	return tl
}

// toInt converts any numeric type to int. Returns -1 if not numeric.
func toInt(v any) int {
	switch n := v.(type) {
	case int:
		return n
	case float64:
		return int(n)
	case float32:
		return int(n)
	default:
		return -1
	}
}

func parseHHMM(s string) int {
	parts := strings.SplitN(s, ":", 2)
	h, m := 0, 0
	fmt.Sscanf(parts[0], "%d", &h)
	if len(parts) > 1 {
		fmt.Sscanf(parts[1], "%d", &m)
	}
	return h*60 + m
}

func fmtHHMM(minutes int) string {
	return fmt.Sprintf("%02d:%02d", minutes/60, minutes%60)
}

// roundUp5 rounds minutes up to the next 5-minute boundary.
func roundUp5(minutes int) int {
	if minutes%5 == 0 {
		return minutes
	}
	return minutes + (5 - minutes%5)
}

// roundTimeUp5 rounds an HH:MM string up to the next 5-minute boundary.
func roundTimeUp5(timeStr string) string {
	return fmtHHMM(roundUp5(parseHHMM(timeStr)))
}

// ── Tool definitions ─────────────────────────────────────

var cueToolDefs = []ToolDef{
	{
		Type: "function",
		Function: ToolDefFunc{
			Name:        "say",
			Description: "Speak text aloud to the user via TTS.",
			Parameters: map[string]any{
				"type":     "object",
				"required": []string{"text"},
				"properties": map[string]any{
					"text": map[string]any{"type": "string", "description": "The text to speak aloud."},
				},
			},
		},
	},
	{
		Type: "function",
		Function: ToolDefFunc{
			Name:        "set_plan",
			Description: "Define the day's plan. Stores items, calculates timeline, and activates the first task. Use this after the user describes their tasks.",
			Parameters: map[string]any{
				"type":     "object",
				"required": []string{"items", "start_time"},
				"properties": map[string]any{
					"items": map[string]any{
						"type":        "array",
						"description": "List of tasks to schedule",
						"items": map[string]any{
							"type":     "object",
							"required": []string{"name", "duration_min"},
							"properties": map[string]any{
								"name":         map[string]any{"type": "string"},
								"duration_min":  map[string]any{"type": "integer"},
								"work_style":    map[string]any{"type": "string", "description": "If omitted, resolved from task_overrides or defaults to 'exploration'."},
							},
						},
					},
					"start_time": map[string]any{"type": "string", "description": "HH:MM start time"},
					"end_time":   map[string]any{"type": "string", "description": "Optional end constraint HH:MM"},
				},
			},
		},
	},
	{
		Type: "function",
		Function: ToolDefFunc{
			Name:        "advance",
			Description: "Mark the current task as done and start the next pending task. Go auto-schedules break timers based on work style. Returns updated plan state.",
			Parameters:  map[string]any{"type": "object", "properties": map[string]any{}},
		},
	},
	{
		Type: "function",
		Function: ToolDefFunc{
			Name:        "calculate_timeline",
			Description: "Compute a full schedule timeline with breaks inserted per work style.",
			Parameters: map[string]any{
				"type":     "object",
				"required": []string{"tasks", "start_time"},
				"properties": map[string]any{
					"tasks": map[string]any{
						"type":        "array",
						"description": "List of tasks to schedule",
						"items": map[string]any{
							"type":     "object",
							"required": []string{"name", "duration_min"},
							"properties": map[string]any{
								"name":         map[string]any{"type": "string"},
								"duration_min":  map[string]any{"type": "integer"},
								"work_style":    map[string]any{"type": "string", "description": "If omitted, resolved from task_overrides."},
							},
						},
					},
					"start_time": map[string]any{"type": "string", "description": "HH:MM"},
					"end_time":   map[string]any{"type": "string", "description": "Optional end constraint HH:MM"},
				},
			},
		},
	},
	{
		Type: "function",
		Function: ToolDefFunc{
			Name:        "manage_work_styles",
			Description: "List, create, update, or delete work style profiles.",
			Parameters: map[string]any{
				"type":     "object",
				"required": []string{"action"},
				"properties": map[string]any{
					"action": map[string]any{"type": "string", "enum": []string{"list", "create", "update", "delete"}},
					"name":   map[string]any{"type": "string"},
					"style":  map[string]any{"type": "object"},
				},
			},
		},
	},
	{
		Type: "function",
		Function: ToolDefFunc{
			Name:        "current_time",
			Description: "Get the current time.",
			Parameters:  map[string]any{"type": "object", "properties": map[string]any{}},
		},
	},
}

// ── Tool dispatch ────────────────────────────────────────

type cueState struct {
	config      *CueConfig
	plan        *CuePlan
	sessDir     string
	timerCh     chan string
	pendingMu   *sync.Mutex
	pendingTimer **time.Timer
}

func cueRunTool(name string, args map[string]any, st *cueState) (any, error) {
	switch name {
	case "say":
		return cueToolSay(args)
	case "set_plan":
		return cueToolSetPlan(args, st)
	case "advance":
		return cueToolAdvance(st)
	case "calculate_timeline":
		return cueToolTimeline(args, st.config)
	case "manage_work_styles":
		return cueToolManageStyles(args, st.config)
	case "current_time":
		return map[string]string{"now": time.Now().Format("15:04")}, nil
	default:
		return map[string]string{"error": fmt.Sprintf("unknown tool '%s'", name)}, nil
	}
}

func cueToolSay(args map[string]any) (any, error) {
	text, _ := args["text"].(string)
	if text == "" {
		return map[string]string{"error": "no text provided"}, nil
	}
	fmt.Fprintf(os.Stderr, "  %s %s\n", Info.Render("say:"), text)
	if err := exec.Command("mini", "say", text).Run(); err != nil {
		return map[string]any{"ok": false, "error": err.Error()}, nil
	}
	return map[string]any{"ok": true}, nil
}

// ── set_plan tool ────────────────────────────────────────

func cueToolSetPlan(args map[string]any, st *cueState) (any, error) {
	itemsRaw, _ := json.Marshal(args["items"])
	var tasks []CueTask
	json.Unmarshal(itemsRaw, &tasks)

	if len(tasks) == 0 {
		return map[string]any{"ok": false, "error": "no items provided"}, nil
	}

	// Resolve work styles
	for i := range tasks {
		if tasks[i].WorkStyle == "" {
			if style, ok := st.config.TaskOverrides[tasks[i].Name]; ok {
				tasks[i].WorkStyle = style
			} else {
				tasks[i].WorkStyle = "exploration"
			}
		}
	}

	// Build plan items
	items := make([]CuePlanItem, len(tasks))
	for i, t := range tasks {
		items[i] = CuePlanItem{
			Name:        t.Name,
			DurationMin: t.DurationMin,
			WorkStyle:   t.WorkStyle,
			Status:      "pending",
		}
	}

	st.plan.Items = items
	st.plan.ActiveIdx = -1

	// Calculate and display timeline
	startTime, _ := args["start_time"].(string)
	if startTime == "" {
		startTime = time.Now().Format("15:04")
	}
	startTime = roundTimeUp5(startTime)
	endTime, _ := args["end_time"].(string)

	tl := cueCalculateTimeline(tasks, st.config.WorkStyles, startTime, endTime)
	cuePrintTimeline(tl)

	// Activate the first item
	cueActivateItem(st, 0, startTime)

	saveCuePlan(st.sessDir, st.plan)

	// Auto-announce via TTS
	if p := loadCuePending(st.sessDir); p != nil {
		delay := minutesUntil(p.NextTime)
		if delay < 0 {
			delay = 0
		}
		text := fmt.Sprintf("Starting %s. %s in %.0f minutes, at %s.",
			st.plan.Items[0].Name, p.EventType, delay, p.NextTime)
		cueToolSay(map[string]any{"text": text})
	}

	return map[string]any{
		"ok":     true,
		"items":  len(items),
		"active": st.plan.Items[0].Name,
	}, nil
}

// ── advance tool ─────────────────────────────────────────

func cueToolAdvance(st *cueState) (any, error) {
	if st.plan.ActiveIdx < 0 || st.plan.ActiveIdx >= len(st.plan.Items) {
		return map[string]any{"ok": false, "error": "no active task to advance"}, nil
	}

	// Mark current as done
	st.plan.Items[st.plan.ActiveIdx].Status = "done"

	// Cancel any pending timer
	cueStopTimer(st)

	// Find next pending item
	nextIdx := -1
	for i := st.plan.ActiveIdx + 1; i < len(st.plan.Items); i++ {
		if st.plan.Items[i].Status == "pending" {
			nextIdx = i
			break
		}
	}

	if nextIdx < 0 {
		st.plan.ActiveIdx = -1
		saveCuePlan(st.sessDir, st.plan)
		cueToolSay(map[string]any{"text": "All tasks done."})
		return map[string]any{
			"ok":      true,
			"message": "all tasks done",
		}, nil
	}

	// Activate next
	nowStr := roundTimeUp5(time.Now().Format("15:04"))
	cueActivateItem(st, nextIdx, nowStr)

	saveCuePlan(st.sessDir, st.plan)

	// Auto-announce via TTS
	if p := loadCuePending(st.sessDir); p != nil {
		delay := minutesUntil(p.NextTime)
		if delay < 0 {
			delay = 0
		}
		text := fmt.Sprintf("Starting %s. %s in %.0f minutes, at %s.",
			st.plan.Items[nextIdx].Name, p.EventType, delay, p.NextTime)
		cueToolSay(map[string]any{"text": text})
	}

	return map[string]any{
		"ok":     true,
		"active": st.plan.Items[nextIdx].Name,
	}, nil
}

// ── Plan helpers ─────────────────────────────────────────

func cueActivateItem(st *cueState, idx int, startTime string) {
	st.plan.Items[idx].Status = "active"
	st.plan.Items[idx].StartedAt = startTime
	st.plan.ActiveIdx = idx

	// Auto-schedule break timer based on work style
	item := st.plan.Items[idx]
	style, ok := st.config.WorkStyles[item.WorkStyle]
	if !ok || style.BreakEveryMin <= 0 {
		// No breaks for this style — schedule task_end only
		endMin := parseHHMM(startTime) + item.DurationMin
		cueScheduleTimer(st, fmtHHMM(endMin), "task_end",
			fmt.Sprintf("[TIMER] task_end — %s is done.", item.Name))
		return
	}

	// First break timer
	breakMin := style.BreakEveryMin
	if breakMin > item.DurationMin {
		breakMin = item.DurationMin
	}
	breakTime := parseHHMM(startTime) + breakMin
	remaining := item.DurationMin - breakMin

	cueScheduleTimer(st, fmtHHMM(breakTime), "break",
		fmt.Sprintf("[TIMER] break — take a %d min break (%s, %dmin elapsed, %dmin remaining)",
			style.BreakDurationMin, item.Name, breakMin, remaining))
}

func cueScheduleTimer(st *cueState, timeStr string, eventType string, message string) {
	delay := minutesUntil(timeStr)
	if delay < 0 {
		delay = 0
	}

	st.pendingMu.Lock()
	if *st.pendingTimer != nil {
		(*st.pendingTimer).Stop()
	}
	*st.pendingTimer = time.AfterFunc(time.Duration(delay*float64(time.Minute)), func() {
		st.timerCh <- message
		saveCuePending(st.sessDir, nil)
	})
	st.pendingMu.Unlock()

	saveCuePending(st.sessDir, &CuePending{NextTime: timeStr, NextLabel: message, EventType: eventType})
	fmt.Fprintf(os.Stderr, "  %s %s at %s (in %.0f min)\n", Info.Render("timer:"), eventType, timeStr, delay)
}

func cueStopTimer(st *cueState) {
	st.pendingMu.Lock()
	if *st.pendingTimer != nil {
		(*st.pendingTimer).Stop()
		*st.pendingTimer = nil
	}
	st.pendingMu.Unlock()
	saveCuePending(st.sessDir, nil)
}

// cueHandleTimerEvent processes a timer message and potentially schedules the next timer.
// Called after the LLM processes a timer event so Go can chain break→resume→break→task_end.
func cueHandleTimerEvent(st *cueState, msg string) {
	if st.plan.ActiveIdx < 0 || st.plan.ActiveIdx >= len(st.plan.Items) {
		return
	}

	item := st.plan.Items[st.plan.ActiveIdx]
	style, ok := st.config.WorkStyles[item.WorkStyle]
	if !ok || style.BreakEveryMin <= 0 {
		return
	}

	now := time.Now()
	nowMin := now.Hour()*60 + now.Minute()
	startMin := parseHHMM(item.StartedAt)
	elapsed := nowMin - startMin
	totalDur := item.DurationMin

	if strings.Contains(msg, "[TIMER] break") {
		// Schedule resume after break duration
		resumeTime := nowMin + style.BreakDurationMin
		remaining := totalDur - elapsed - style.BreakDurationMin
		if remaining < 0 {
			remaining = 0
		}
		cueScheduleTimer(st, fmtHHMM(resumeTime), "resume",
			fmt.Sprintf("[TIMER] resume — back to %s (%dmin remaining)", item.Name, remaining))
	} else if strings.Contains(msg, "[TIMER] resume") {
		// How much work time left after this resume?
		remaining := totalDur - elapsed
		if remaining <= 0 {
			// Task is done
			cueScheduleTimer(st, fmtHHMM(nowMin), "task_end",
				fmt.Sprintf("[TIMER] task_end — %s is done.", item.Name))
			return
		}
		// Next break or task_end, whichever comes first
		nextChunk := style.BreakEveryMin
		if nextChunk > remaining {
			nextChunk = remaining
		}
		nextTime := nowMin + nextChunk
		if nextChunk >= remaining {
			cueScheduleTimer(st, fmtHHMM(nextTime), "task_end",
				fmt.Sprintf("[TIMER] task_end — %s is done.", item.Name))
		} else {
			newRemaining := remaining - nextChunk
			cueScheduleTimer(st, fmtHHMM(nextTime), "break",
				fmt.Sprintf("[TIMER] break — take a %d min break (%s, %dmin elapsed, %dmin remaining)",
					style.BreakDurationMin, item.Name, elapsed+nextChunk, newRemaining))
		}
	}
	// task_end events don't chain — the LLM should call advance() or say()
}

func cuePlanSummary(plan *CuePlan) []map[string]string {
	var summary []map[string]string
	for _, item := range plan.Items {
		m := map[string]string{
			"name":   item.Name,
			"status": item.Status,
		}
		if item.StartedAt != "" {
			m["started_at"] = item.StartedAt
		}
		summary = append(summary, m)
	}
	return summary
}

// ── Other tools ──────────────────────────────────────────

func cueToolTimeline(args map[string]any, cc *CueConfig) (any, error) {
	tasksRaw, _ := json.Marshal(args["tasks"])
	var tasks []CueTask
	json.Unmarshal(tasksRaw, &tasks)

	// Resolve work_style from task_overrides
	for i := range tasks {
		if tasks[i].WorkStyle == "" {
			if style, ok := cc.TaskOverrides[tasks[i].Name]; ok {
				tasks[i].WorkStyle = style
			}
		}
	}

	startTime, _ := args["start_time"].(string)
	endTime, _ := args["end_time"].(string)

	tl := cueCalculateTimeline(tasks, cc.WorkStyles, startTime, endTime)
	cuePrintTimeline(tl)
	return tl, nil
}

func cuePrintTimeline(tl CueTimeline) {
	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, Sep(40))
	for _, e := range tl.Timeline {
		var label string
		switch e.Type {
		case "task_start":
			label = Ready.Render(e.Task)
		case "task_end":
			label = Dim.Render(e.Task + " done")
		case "break":
			label = Active.Render("break")
		case "resume":
			label = Info.Render("resume " + e.Task)
		default:
			label = e.Type
		}
		fmt.Fprintf(os.Stderr, "  %s  %s\n", Dim.Render(e.Time), label)
	}
	fmt.Fprintln(os.Stderr, Sep(40))
	summary := fmt.Sprintf("%s work, %s break, ends %s",
		fmtMinLabel(tl.TotalWorkMin), fmtMinLabel(tl.TotalBreakMin), tl.EndTime)
	if !tl.Fits {
		summary += fmt.Sprintf(" (%d min over)", tl.OverflowMin)
	}
	fmt.Fprintf(os.Stderr, "  %s\n", Info.Render(summary))
	if tl.Warning != "" {
		fmt.Fprintf(os.Stderr, "  %s\n", Blocked.Render(tl.Warning))
	}
	fmt.Fprintln(os.Stderr)
}

func fmtMinLabel(min int) string {
	if min >= 60 && min%60 == 0 {
		return fmt.Sprintf("%dh", min/60)
	}
	if min >= 60 {
		return fmt.Sprintf("%dh%dm", min/60, min%60)
	}
	return fmt.Sprintf("%dm", min)
}

func cueToolManageStyles(args map[string]any, cc *CueConfig) (any, error) {
	action, _ := args["action"].(string)

	switch action {
	case "list":
		return map[string]any{"styles": cc.WorkStyles}, nil

	case "create":
		name, _ := args["name"].(string)
		if _, exists := cc.WorkStyles[name]; exists {
			return map[string]any{"ok": false, "error": fmt.Sprintf("style '%s' already exists", name)}, nil
		}
		var style CueWorkStyle
		if styleMap, ok := args["style"].(map[string]any); ok {
			if v, ok := styleMap["description"].(string); ok {
				style.Description = v
			}
			if v := toInt(styleMap["break_every_min"]); v >= 0 {
				style.BreakEveryMin = v
			}
			if v := toInt(styleMap["break_duration_min"]); v >= 0 {
				style.BreakDurationMin = v
			}
			if v, ok := styleMap["examples"].([]any); ok {
				for _, e := range v {
					if s, ok := e.(string); ok {
						style.Examples = append(style.Examples, s)
					}
				}
			}
		}
		cc.WorkStyles[name] = style
		saveCueConfig(*cc)
		names := make([]string, 0, len(cc.WorkStyles))
		for k := range cc.WorkStyles {
			names = append(names, k)
		}
		return map[string]any{"ok": true, "styles": names}, nil

	case "update":
		name, _ := args["name"].(string)
		if _, exists := cc.WorkStyles[name]; !exists {
			return map[string]any{"ok": false, "error": fmt.Sprintf("style '%s' not found", name)}, nil
		}
		existing := cc.WorkStyles[name]
		if styleMap, ok := args["style"].(map[string]any); ok {
			if v, ok := styleMap["description"].(string); ok {
				existing.Description = v
			}
			if v := toInt(styleMap["break_every_min"]); v >= 0 {
				existing.BreakEveryMin = v
			}
			if v := toInt(styleMap["break_duration_min"]); v >= 0 {
				existing.BreakDurationMin = v
			}
			if v, ok := styleMap["examples"].([]any); ok {
				existing.Examples = nil
				for _, e := range v {
					if s, ok := e.(string); ok {
						existing.Examples = append(existing.Examples, s)
					}
				}
			}
		}
		cc.WorkStyles[name] = existing
		saveCueConfig(*cc)
		return map[string]any{"ok": true}, nil

	case "delete":
		name, _ := args["name"].(string)
		if _, exists := cc.WorkStyles[name]; !exists {
			return map[string]any{"ok": false, "error": fmt.Sprintf("style '%s' not found", name)}, nil
		}
		delete(cc.WorkStyles, name)
		saveCueConfig(*cc)
		return map[string]any{"ok": true}, nil
	}

	return map[string]any{"ok": false, "error": fmt.Sprintf("unknown action '%s'", action)}, nil
}

// ── Dynamic system prompt ────────────────────────────────

func buildCueSystem(cc CueConfig, plan *CuePlan, sessDir string) string {
	var b strings.Builder

	fmt.Fprintf(&b, "You are Cue, a scheduling assistant. Be brief.\nCurrent time: %s\n", time.Now().Format("15:04:05"))

	// Plan state
	if len(plan.Items) > 0 {
		b.WriteString("\n## Plan\n")
		for i, item := range plan.Items {
			fmt.Fprintf(&b, "%d. [%s] %s (%dmin, %s)", i+1, item.Status, item.Name, item.DurationMin, item.WorkStyle)
			if item.Status == "active" && item.StartedAt != "" {
				now := time.Now()
				nowMin := now.Hour()*60 + now.Minute()
				elapsed := nowMin - parseHHMM(item.StartedAt)
				if elapsed < 0 {
					elapsed = 0
				}
				remaining := item.DurationMin - elapsed
				if remaining < 0 {
					remaining = 0
				}
				fmt.Fprintf(&b, " — started %s, %dmin elapsed, %dmin left", item.StartedAt, elapsed, remaining)
			}
			b.WriteString("\n")
		}
	}

	// Pending timer
	if p := loadCuePending(sessDir); p != nil {
		delay := minutesUntil(p.NextTime)
		if delay < 0 {
			delay = 0
		}
		fmt.Fprintf(&b, "\nNext timer: %s at %s (in %.0f min) — Go will send a [TIMER] message when it fires. Do NOT announce it early.\n", p.EventType, p.NextTime, delay)
	}

	// Work styles
	b.WriteString("\n## Work styles\n")
	for name, s := range cc.WorkStyles {
		fmt.Fprintf(&b, "- **%s**: %s", name, s.Description)
		if s.BreakEveryMin > 0 {
			fmt.Fprintf(&b, " (break every %dm, %dm break)", s.BreakEveryMin, s.BreakDurationMin)
		} else {
			b.WriteString(" (no breaks)")
		}
		b.WriteString("\n")
	}

	// Task overrides
	if len(cc.TaskOverrides) > 0 {
		b.WriteString("\nTask overrides: ")
		parts := make([]string, 0, len(cc.TaskOverrides))
		for task, style := range cc.TaskOverrides {
			parts = append(parts, fmt.Sprintf("%q→%s", task, style))
		}
		b.WriteString(strings.Join(parts, ", "))
		b.WriteString("\n")
	}

	b.WriteString(`
## Tools
- set_plan(items, start_time): set the plan. Each item needs "name" (string), "duration_min" (integer), and optionally "work_style" (string). start_time is HH:MM. This shows the timeline, activates the first task, and starts break timers automatically.
- advance(): mark current task done, start next. Go auto-schedules breaks.
- say(text): speak via TTS.
- calculate_timeline: preview schedule without committing (set_plan already does this).
- manage_work_styles: CRUD for work style profiles.
- current_time: get time.

## Rules
- When the user states tasks with durations, call set_plan immediately using current time as start_time. set_plan auto-announces via TTS — you don't need to call say() after it.
- If a duration is ambiguous ("1 or 2 hours"), ask which one. But "hour of linux" = 60min, "2 hours music" = 120min — these are clear.
- Don't repeat the timeline — it's shown visually. Don't call say() after set_plan or advance — they announce automatically.
- Timers are managed by Go. You will receive [TIMER] messages when events fire. Do NOT pre-announce future events.
- On [TIMER] break: call say() to announce the break.
- On [TIMER] resume: call say() to announce resuming.
- On [TIMER] task_end: call advance(). advance auto-announces via TTS.
- For user questions or conversation, use say() to respond.
- Work style is auto-resolved from task overrides. Don't ask about it.
`)

	return b.String()
}

// cueSpinner shows a smooth scanning bar animation.
func cueSpinner() func() {
	if !isTerminal() {
		return func() {}
	}
	// Scanning light across a bar: ━ is dim, ═ or bright ━ is the highlight
	barWidth := 12
	done := make(chan struct{})
	go func() {
		for {
			select {
			case <-done:
				fmt.Fprintf(os.Stderr, "\r%s\r", strings.Repeat(" ", barWidth+4))
				return
			default:
				pos := int(time.Now().UnixMilli()/80) % (barWidth * 2)
				if pos >= barWidth {
					pos = barWidth*2 - pos - 1
				}
				var bar strings.Builder
				for i := 0; i < barWidth; i++ {
					dist := pos - i
					if dist < 0 {
						dist = -dist
					}
					switch {
					case dist == 0:
						bar.WriteString("━")
					case dist == 1:
						bar.WriteString("─")
					default:
						bar.WriteString(" ")
					}
				}
				fmt.Fprintf(os.Stderr, "\r  %s", Dim.Render(bar.String()))
				time.Sleep(40 * time.Millisecond)
			}
		}
	}()
	return func() { close(done); time.Sleep(10 * time.Millisecond) }
}

// ── Event loop ───────────────────────────────────────────

func cueOneTurn(sess *Session, client LLMClient, model string, input string, st *cueState) string {
	userEntry := HistoryEntry{Role: "user", Content: input, Time: time.Now().UTC().Format(time.RFC3339)}
	sess.History = append(sess.History, userEntry)
	sess.AppendHistory(userEntry)

	const maxIter = 10
	for iter := 0; iter < maxIter; iter++ {
		// Rebuild system prompt with current plan state every iteration
		sess.System = buildCueSystem(*st.config, st.plan, st.sessDir)

		stop := cueSpinner()
		resp, err := client.Chat(ChatRequest{
			Model:    model,
			Messages: sess.Messages(),
			Tools:    cueToolDefs,
		})
		stop()

		if err != nil {
			msg := fmt.Sprintf("Error: %v", err)
			fmt.Fprintln(os.Stderr, Blocked.Render(msg))
			return msg
		}

		// Check for native tool_calls
		toolCalls := resp.Message.ToolCalls

		// Fallback: parse tool call JSON from content (for models that don't use native tool_calls)
		if len(toolCalls) == 0 && resp.Message.Content != "" {
			content := strings.TrimSpace(resp.Message.Content)
			var parsed map[string]any
			if json.Unmarshal([]byte(content), &parsed) == nil {
				if _, hasName := parsed["name"]; hasName {
					args, _ := parsed["arguments"].(map[string]any)
					if args == nil {
						args = map[string]any{}
					}
					toolCalls = []ToolCall{{Function: ToolCallFunction{
						Name:      parsed["name"].(string),
						Arguments: args,
					}}}
				}
			}
		}

		// Append assistant response
		asstEntry := HistoryEntry{Role: "assistant", Content: resp.Message.Content, ToolCalls: toolCalls, Time: time.Now().UTC().Format(time.RFC3339)}
		sess.History = append(sess.History, asstEntry)
		sess.AppendHistory(asstEntry)

		if len(toolCalls) > 0 {
			terminal := false
			for _, tc := range toolCalls {
				argsJSON, _ := json.Marshal(tc.Function.Arguments)
				fmt.Fprintf(os.Stderr, "  %s %s(%s)\n", Dim.Render("tool:"), tc.Function.Name, string(argsJSON))
				result, _ := cueRunTool(tc.Function.Name, tc.Function.Arguments, st)
				resultJSON, _ := json.Marshal(result)
				toolEntry := HistoryEntry{Role: "tool", Content: string(resultJSON), Time: time.Now().UTC().Format(time.RFC3339)}
				sess.History = append(sess.History, toolEntry)
				sess.AppendHistory(toolEntry)
				// These tools call say() internally and arm timers — turn is done
				switch tc.Function.Name {
				case "say", "set_plan", "advance":
					terminal = true
				}
			}
			// Save plan after tool execution
			saveCuePlan(st.sessDir, st.plan)
			if terminal {
				return ""
			}
			continue // loop back for more tool calls or final text
		}

		// No tool calls — text response, print and return
		if resp.Message.Content != "" {
			fmt.Println()
			fmt.Println(resp.Message.Content)
			fmt.Println()
		}

		return resp.Message.Content
	}

	// Max iterations reached — safety cap
	fmt.Fprintln(os.Stderr, Blocked.Render("  (tool loop limit reached)"))
	return ""
}

func cueEventLoop(sess *Session, client LLMClient, model string, st *cueState, initialPrompt string) {
	// Check for missed timer on resume
	if pending := loadCuePending(st.sessDir); pending != nil {
		delay := minutesUntil(pending.NextTime)
		if delay < 0 {
			// Missed — inject as event
			fmt.Fprintf(os.Stderr, "  %s %s\n", Info.Render("missed timer:"), pending.NextLabel)
			saveCuePending(st.sessDir, nil)
			resp := cueOneTurn(sess, client, model, pending.NextLabel, st)
			_ = resp
			cueHandleTimerEvent(st, pending.NextLabel)
		} else {
			// Still in the future — re-arm
			fmt.Fprintf(os.Stderr, "  %s %s at %s (in %.0f min)\n", Info.Render("resuming timer:"), pending.EventType, pending.NextTime, delay)
			label := pending.NextLabel
			st.pendingMu.Lock()
			*st.pendingTimer = time.AfterFunc(time.Duration(delay*float64(time.Minute)), func() {
				st.timerCh <- label
				saveCuePending(st.sessDir, nil)
			})
			st.pendingMu.Unlock()
		}
	}

	// Handle initial prompt if provided
	if initialPrompt != "" {
		cueOneTurn(sess, client, model, initialPrompt, st)
	}

	// Read stdin in a goroutine
	inputCh := make(chan string, 1)
	go func() {
		scanner := bufio.NewScanner(os.Stdin)
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if line != "" {
				inputCh <- line
			}
		}
		close(inputCh)
	}()

	for {
		fmt.Fprint(os.Stderr, Info.Render("cue> "))

		select {
		case timerMsg := <-st.timerCh:
			fmt.Fprintf(os.Stderr, "\n  %s %s\n", Active.Render("timer:"), timerMsg)
			cueOneTurn(sess, client, model, timerMsg, st)
			// Chain next timer based on event type
			cueHandleTimerEvent(st, timerMsg)

		case input, ok := <-inputCh:
			if !ok {
				// stdin closed
				return
			}
			if input == "quit" || input == "exit" {
				return
			}
			cueOneTurn(sess, client, model, input, st)
		}
	}
}

func minutesUntil(timeStr string) float64 {
	now := time.Now()
	h, m := 0, 0
	fmt.Sscanf(timeStr, "%d:%d", &h, &m)
	target := time.Date(now.Year(), now.Month(), now.Day(), h, m, 0, 0, now.Location())
	return target.Sub(now).Minutes()
}

// ── Command ──────────────────────────────────────────────

func cueCmd() *cobra.Command {
	var (
		model   string
		session string
	)

	cmd := &cobra.Command{
		Use:   "cue [PROMPT]",
		Short: "Voice-first scheduling assistant",
		Long: `Describe your day conversationally. Cue builds a feasible schedule
with break management, then runs it with TTS notifications.

  mini cue "hour of linux then two hours of web3, start at 10"
  mini cue                                  # interactive mode
  mini cue -S my-day "hour of coding"       # named session (resumable)`,
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cc := loadCueConfig()

			if model == "" {
				model = cc.Model
			}
			client, cleanModel, err := llmClient(model)
			if err != nil {
				return err
			}

			// Session name: explicit, or auto-generated
			sessName := session
			if sessName == "" {
				sessName = "cue-" + GenerateSlug()
			}

			sess, err := OpenSession(sessName)
			if err != nil {
				return err
			}

			// Load or create plan
			plan := loadCuePlan(sess.Dir)

			// Build initial system prompt
			system := buildCueSystem(cc, plan, sess.Dir)
			sess.System = system
			os.WriteFile(filepath.Join(sess.Dir, "system.md"), []byte(system), 0644)

			fmt.Println()
			fmt.Println(Selected.Render("  mini cue"))
			fmt.Println(Sep(50))
			fmt.Println(Row("model", cleanModel))
			fmt.Println(Row("session", sessName))
			fmt.Println()

			// Set up timer infrastructure
			timerCh := make(chan string, 1)
			var pendingTimer *time.Timer
			var pendingMu sync.Mutex

			st := &cueState{
				config:       &cc,
				plan:         plan,
				sessDir:      sess.Dir,
				timerCh:      timerCh,
				pendingMu:    &pendingMu,
				pendingTimer: &pendingTimer,
			}

			// Initial prompt from args
			var initialPrompt string
			if len(args) > 0 {
				initialPrompt = strings.Join(args, " ")
			}

			cueEventLoop(sess, client, cleanModel, st, initialPrompt)

			return nil
		},
	}

	cmd.Flags().StringVarP(&model, "model", "m", "", "model override")
	cmd.Flags().StringVarP(&session, "session", "S", "", "session name (for resume)")

	return cmd
}
