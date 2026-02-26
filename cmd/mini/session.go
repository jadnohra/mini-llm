package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// ── types ──────────────────────────────────────────────

type SessionConfig struct {
	Model        string  `yaml:"model"`
	Temperature  float64 `yaml:"temperature"`
	MaxTokens    int     `yaml:"max_tokens"`
	ContextLimit int     `yaml:"context_limit"`
}

type HistoryEntry struct {
	Role      string     `json:"role"`
	Content   string     `json:"content"`
	ToolCalls []ToolCall `json:"tool_calls,omitempty"`
	Tokens    int        `json:"tokens,omitempty"`
	Turn      int        `json:"turn,omitempty"`
	TokS      float64    `json:"tok_s,omitempty"`
	Time      string     `json:"time,omitempty"`
}

type Session struct {
	Name    string
	Dir     string
	Config  SessionConfig
	System  string
	History []HistoryEntry
}

// ── slug generation ────────────────────────────────────

var slugAnimals = []string{
	"wombat", "narwhal", "capybara", "pangolin", "platypus",
	"axolotl", "quokka", "okapi", "tardigrade", "chinchilla",
	"armadillo", "manatee", "flamingo", "puffin", "sloth",
	"chameleon", "hedgehog", "otter", "pelican", "lemur",
}

var slugNumbers = []int{
	42, 404, 1729, 314, 1337, 256, 1024, 2718, 8080, 443,
	137, 196, 451, 666, 007, 101, 360, 747, 911, 1984,
}

func GenerateSlug() string {
	for range 100 {
		animal := slugAnimals[rand.Intn(len(slugAnimals))]
		number := slugNumbers[rand.Intn(len(slugNumbers))]
		name := fmt.Sprintf("%s-%d", animal, number)
		if !SessionExists(name) {
			return name
		}
	}
	// Fallback: append timestamp
	animal := slugAnimals[rand.Intn(len(slugAnimals))]
	return fmt.Sprintf("%s-%d", animal, time.Now().Unix())
}

// SessionExists checks if a session with this name exists.
func SessionExists(name string) bool {
	base, err := sessionsDir()
	if err != nil {
		return false
	}
	_, err = os.Stat(filepath.Join(base, name))
	return err == nil
}

// DeleteSession removes a session directory entirely.
func DeleteSession(name string) error {
	if err := validateSessionName(name); err != nil {
		return err
	}
	base, err := sessionsDir()
	if err != nil {
		return err
	}
	dir := filepath.Join(base, name)
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		return fmt.Errorf("session %q not found", name)
	}
	return os.RemoveAll(dir)
}

// ── open / create ──────────────────────────────────────

func sessionsDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(home, ".mini", "sessions")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return dir, nil
}

func validateSessionName(name string) error {
	if strings.Contains(name, "/") || strings.Contains(name, "..") {
		return fmt.Errorf("invalid session name %q", name)
	}
	if strings.HasPrefix(name, "-") {
		return fmt.Errorf("session name cannot start with -")
	}
	if name == "" {
		return fmt.Errorf("session name cannot be empty")
	}
	return nil
}

// OpenSession opens an existing session or creates one if it doesn't exist.
func OpenSession(name string) (*Session, error) {
	if err := validateSessionName(name); err != nil {
		return nil, err
	}

	base, err := sessionsDir()
	if err != nil {
		return nil, err
	}

	dir := filepath.Join(base, name)
	s := &Session{Name: name, Dir: dir}

	// Auto-create if it doesn't exist
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		if err := initSession(dir); err != nil {
			return nil, fmt.Errorf("create session: %w", err)
		}
	}

	if err := s.loadConfig(); err != nil {
		return nil, err
	}
	s.loadSystem()
	if err := s.loadHistory(); err != nil {
		return nil, err
	}

	return s, nil
}

func initSession(dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}

	sc := SessionConfig{
		Model:        cfg.DefaultModel,
		Temperature:  0.3,
		MaxTokens:    4096,
		ContextLimit: 131072,
	}
	data, err := yaml.Marshal(sc)
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "model.yaml"), data, 0o644); err != nil {
		return err
	}

	// Empty system.md
	return os.WriteFile(filepath.Join(dir, "system.md"), nil, 0o644)
}

// ── config ─────────────────────────────────────────────

func (s *Session) loadConfig() error {
	data, err := os.ReadFile(filepath.Join(s.Dir, "model.yaml"))
	if err != nil {
		return fmt.Errorf("read model.yaml: %w", err)
	}
	return yaml.Unmarshal(data, &s.Config)
}

func (s *Session) saveConfig() error {
	data, err := yaml.Marshal(s.Config)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(s.Dir, "model.yaml"), data, 0o644)
}

// ── system prompt ──────────────────────────────────────

func (s *Session) loadSystem() {
	data, _ := os.ReadFile(filepath.Join(s.Dir, "system.md"))
	s.System = strings.TrimSpace(string(data))
}

// ── history ────────────────────────────────────────────

func (s *Session) loadHistory() error {
	path := filepath.Join(s.Dir, "history.jsonl")
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	// Allow large lines (responses can be big)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}
		var entry HistoryEntry
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			// Tolerate malformed final line (crash recovery)
			fmt.Fprintf(os.Stderr, "warning: skipping malformed history line\n")
			continue
		}
		s.History = append(s.History, entry)
	}
	return scanner.Err()
}

func (s *Session) AppendHistory(entry HistoryEntry) error {
	path := filepath.Join(s.Dir, "history.jsonl")
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()

	data, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(f, "%s\n", data)
	return err
}

// ── message building ───────────────────────────────────

func (s *Session) Messages() []ChatMessage {
	var msgs []ChatMessage
	if s.System != "" {
		msgs = append(msgs, ChatMessage{Role: "system", Content: s.System})
	}
	for _, e := range s.History {
		msgs = append(msgs, ChatMessage{Role: e.Role, Content: e.Content, ToolCalls: e.ToolCalls})
	}
	return msgs
}

// ── listing ────────────────────────────────────────────

type SessionInfo struct {
	Name     string
	Turns    int
	Messages int
	LastUsed time.Time
	FirstMsg string // first user message (truncated)
	LastMsg  string // last message of any role (truncated)
}

func ListSessions() ([]SessionInfo, error) {
	base, err := sessionsDir()
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(base)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var sessions []SessionInfo
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dir := filepath.Join(base, e.Name())
		info := SessionInfo{Name: e.Name()}

		// Count turns/messages and capture first/last from history.jsonl
		histPath := filepath.Join(dir, "history.jsonl")
		if f, err := os.Open(histPath); err == nil {
			scanner := bufio.NewScanner(f)
			scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
			for scanner.Scan() {
				line := scanner.Text()
				if line == "" {
					continue
				}
				var entry HistoryEntry
				if json.Unmarshal([]byte(line), &entry) != nil {
					continue
				}
				info.Messages++
				if entry.Role == "user" {
					info.Turns++
					if info.FirstMsg == "" {
						info.FirstMsg = firstLine(entry.Content, 60)
					}
				}
				info.LastMsg = firstLine(entry.Content, 60)
				if entry.Time != "" {
					if t, err := time.Parse(time.RFC3339, entry.Time); err == nil {
						info.LastUsed = t
					}
				}
			}
			f.Close()
		}

		// Fallback: use dir modification time
		if info.LastUsed.IsZero() {
			if fi, err := e.Info(); err == nil {
				info.LastUsed = fi.ModTime()
			}
		}

		sessions = append(sessions, info)
	}
	return sessions, nil
}

// firstLine returns the first line of s, truncated to max characters.
func firstLine(s string, max int) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	s = strings.TrimSpace(s)
	if len(s) > max {
		return s[:max-1] + "…"
	}
	return s
}

// ── message building ───────────────────────────────────

func (s *Session) Turn() int {
	n := 0
	for _, e := range s.History {
		if e.Role == "user" {
			n++
		}
	}
	return n
}
