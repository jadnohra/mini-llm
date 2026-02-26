package main

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/term"
)

// Terminus palette — muted 256-color, no emoji
var (
	Tree     = lipgloss.NewStyle().Foreground(lipgloss.Color("247"))
	Path     = lipgloss.NewStyle().Foreground(lipgloss.Color("253"))
	Info     = lipgloss.NewStyle().Foreground(lipgloss.Color("249"))
	Help     = lipgloss.NewStyle().Foreground(lipgloss.Color("247"))
	Selected = lipgloss.NewStyle().Bold(true)
	Ready    = lipgloss.NewStyle().Foreground(lipgloss.Color("70"))
	Blocked  = lipgloss.NewStyle().Foreground(lipgloss.Color("131"))
	Done     = lipgloss.NewStyle().Foreground(lipgloss.Color("249"))
	Active   = lipgloss.NewStyle().Foreground(lipgloss.Color("214"))
	Dim      = lipgloss.NewStyle().Foreground(lipgloss.Color("247"))
	Label    = lipgloss.NewStyle().Foreground(lipgloss.Color("253")).Width(16)
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

func termWidth() int {
	w, _, err := term.GetSize(os.Stderr.Fd())
	if err != nil || w <= 0 {
		return 80
	}
	return w
}

func centerText(text string) string {
	w := termWidth()
	visibleWidth := lipgloss.Width(text)
	pad := (w - visibleWidth) / 2
	if pad <= 0 {
		return text
	}
	return strings.Repeat(" ", pad) + text
}

func isTerminal() bool {
	_, _, err := term.GetSize(os.Stderr.Fd())
	return err == nil
}

// Spinner shows a centered animated spinner on stderr, returns a function to stop it.
// When stderr is not a TTY (piped), emits a single status line instead of animation.
func Spinner(msg string) func() {
	if !isTerminal() {
		fmt.Fprintf(os.Stderr, "%s\n", msg)
		return func() {}
	}
	done := make(chan struct{})
	go func() {
		for {
			select {
			case <-done:
				// Clear the spinner line
				fmt.Fprintf(os.Stderr, "\r%s\r", strings.Repeat(" ", termWidth()))
				return
			default:
				frame := pulseFrames[int(time.Now().UnixMilli()/120)%len(pulseFrames)]
				line := Active.Render(frame) + " " + Info.Render(msg)
				fmt.Fprintf(os.Stderr, "\r%s", centerText(line))
				time.Sleep(80 * time.Millisecond)
			}
		}
	}()
	return func() { close(done); time.Sleep(10 * time.Millisecond) }
}

// Tree branch characters
func Branch(last bool) string {
	if last {
		return Tree.Render("└─ ")
	}
	return Tree.Render("├─ ")
}
