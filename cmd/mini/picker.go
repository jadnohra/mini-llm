package main

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/charmbracelet/x/term"
)

// Pick shows an interactive arrow-key picker on stderr.
// Returns the index of the selected item, or -1 if cancelled (Ctrl+C / Esc).
func Pick(items []string, selected int) int {
	if len(items) == 0 {
		return -1
	}
	if selected < 0 || selected >= len(items) {
		selected = 0
	}

	// Switch stdin to raw mode
	fd := os.Stdin.Fd()
	oldState, err := term.MakeRaw(fd)
	if err != nil {
		// Fallback: can't do raw mode, just return first item
		return 0
	}
	defer term.Restore(fd, oldState)

	// Hide cursor
	fmt.Fprint(os.Stderr, "\033[?25l")
	defer fmt.Fprint(os.Stderr, "\033[?25h")

	render := func() {
		for i, item := range items {
			if i == selected {
				fmt.Fprintf(os.Stderr, "  %s %s\r\n", Active.Render(">"), Selected.Render(item))
			} else {
				fmt.Fprintf(os.Stderr, "    %s\r\n", Info.Render(item))
			}
		}
	}

	clear := func() {
		// Move up and clear each line
		for range items {
			fmt.Fprint(os.Stderr, "\033[A\033[2K")
		}
	}

	render()

	buf := make([]byte, 32)
	for {
		n, err := os.Stdin.Read(buf)
		if err != nil || n == 0 {
			clear()
			return -1
		}
		b := buf[:n]

		switch {
		case n == 1 && b[0] == 13: // Enter
			clear()
			return selected
		case n == 1 && b[0] == 3: // Ctrl+C
			clear()
			return -1
		case n == 1 && b[0] == 27: // Esc (standalone)
			clear()
			return -1
		case n >= 3 && b[0] == 27 && b[1] == 91: // Escape sequence \x1b[X
			switch b[2] {
			case 'A': // Up
				clear()
				if selected > 0 {
					selected--
				}
				render()
			case 'B': // Down
				clear()
				if selected < len(items)-1 {
					selected++
				}
				render()
			}
		case n == 1 && b[0] == 'k': // vim up
			clear()
			if selected > 0 {
				selected--
			}
			render()
		case n == 1 && b[0] == 'j': // vim down
			clear()
			if selected < len(items)-1 {
				selected++
			}
			render()
		case n == 1 && b[0] == 'q': // quit
			clear()
			return -1
		}
	}
}

// PickSession shows a session picker with recent sessions + "new".
// Returns session name or "" if cancelled.
func PickSession() (string, error) {
	sessions, err := ListSessions()
	if err != nil {
		return "", err
	}

	// Sort newest first
	sort.Slice(sessions, func(i, j int) bool {
		return sessions[i].LastUsed.After(sessions[j].LastUsed)
	})

	// Cap at 20
	if len(sessions) > 20 {
		sessions = sessions[:20]
	}

	// Build display items
	items := make([]string, 0, len(sessions)+1)
	names := make([]string, 0, len(sessions)+1)
	for _, s := range sessions {
		age := shortAge(s.LastUsed)
		turns := fmt.Sprintf("%d turns", s.Turns)
		if s.Turns == 0 {
			turns = "empty"
		}
		label := fmt.Sprintf("%-24s %8s  %s", s.Name, turns, age)
		if s.FirstMsg != "" {
			label += "  " + Dim.Render(truncate(s.FirstMsg, 40))
		}
		items = append(items, label)
		names = append(names, s.Name)
	}
	items = append(items, Dim.Render("+ new session"))
	names = append(names, "")

	fmt.Fprintf(os.Stderr, "\n%s\n", Info.Render("  select session:"))
	idx := Pick(items, 0)
	if idx < 0 {
		return "", fmt.Errorf("cancelled")
	}
	if names[idx] == "" {
		// New session
		return GenerateSlug(), nil
	}
	return names[idx], nil
}

func truncate(s string, max int) string {
	s = strings.TrimSpace(s)
	if len(s) > max {
		return s[:max-1] + "…"
	}
	return s
}
