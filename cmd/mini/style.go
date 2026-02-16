package main

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
)

// Terminus palette — muted 256-color, no emoji
var (
	Tree     = lipgloss.NewStyle().Foreground(lipgloss.Color("238"))
	Path     = lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
	Info     = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
	Help     = lipgloss.NewStyle().Foreground(lipgloss.Color("236"))
	Selected = lipgloss.NewStyle().Bold(true)
	Ready    = lipgloss.NewStyle().Foreground(lipgloss.Color("70"))
	Blocked  = lipgloss.NewStyle().Foreground(lipgloss.Color("131"))
	Done     = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
	Active   = lipgloss.NewStyle().Foreground(lipgloss.Color("214"))
	Dim      = lipgloss.NewStyle().Foreground(lipgloss.Color("238"))
	Label    = lipgloss.NewStyle().Foreground(lipgloss.Color("245")).Width(16)
)

// Pulse star animation frames
var pulseFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

func PulseFrame() string {
	idx := int(time.Now().UnixMilli()/200) % len(pulseFrames)
	return Active.Render(pulseFrames[idx])
}

// Status indicators — unicode only, no emoji
func StatusOK(msg string) string   { return Ready.Render("● ") + msg }
func StatusFail(msg string) string { return Blocked.Render("✗ ") + msg }
func StatusDone(msg string) string { return Done.Render("✓ ") + msg }
func StatusInfo(msg string) string { return Info.Render("· ") + msg }

// Separator line
func Sep(width int) string {
	return Dim.Render(strings.Repeat("─", width))
}

// Format a labeled row: "  Label:       value"
func Row(label, value string) string {
	return fmt.Sprintf("  %s %s", Label.Render(label), value)
}

// Format bytes to human-readable
func FmtSize(bytes int64) string {
	gb := float64(bytes) / (1024 * 1024 * 1024)
	if gb >= 1.0 {
		return fmt.Sprintf("%.1f GB", gb)
	}
	mb := float64(bytes) / (1024 * 1024)
	return fmt.Sprintf("%.0f MB", mb)
}

// Format tok/s
func FmtTokS(count int, durNano int64) string {
	if durNano <= 0 {
		return "—"
	}
	tps := float64(count) / (float64(durNano) / 1e9)
	return fmt.Sprintf("%.1f tok/s", tps)
}

// Animated spinner — runs in background on stderr, returns stop function
func Spinner(msg string) func() {
	done := make(chan struct{})
	go func() {
		i := 0
		for {
			select {
			case <-done:
				fmt.Fprintf(os.Stderr, "\r\033[K")
				return
			default:
				frame := Active.Render(pulseFrames[i%len(pulseFrames)])
				fmt.Fprintf(os.Stderr, "\r%s %s", frame, Info.Render(msg))
				i++
				time.Sleep(80 * time.Millisecond)
			}
		}
	}()
	return func() { close(done); time.Sleep(50 * time.Millisecond) }
}

// Tree branch characters
func Branch(last bool) string {
	if last {
		return Tree.Render("└─ ")
	}
	return Tree.Render("├─ ")
}
