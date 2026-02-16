package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

func ollamaClient() *OllamaClient {
	t, err := ensureTunnel()
	if err != nil {
		// Fall back to direct connection
		return NewOllamaClient(cfg.OllamaURL())
	}
	return NewOllamaClient(t.LocalURL())
}
func sshClient() *SSHClient { return NewSSHClient(cfg.SSHUser, cfg.Host) }

// ── mini status ────────────────────────────────────────

type StatusResult struct {
	Ollama   *ServiceStatus `json:"ollama"`
	LlamaCPP *ServiceStatus `json:"llamacpp"`
	Memory   string         `json:"memory,omitempty"`
	Disk     string         `json:"disk,omitempty"`
	Load     string         `json:"load,omitempty"`
	SSHError string         `json:"ssh_error,omitempty"`
}

type ServiceStatus struct {
	Running bool   `json:"running"`
	Models  int    `json:"models,omitempty"`
	Error   string `json:"error,omitempty"`
}

func statusCmd() *cobra.Command {
	var jsonOut bool

	cmd := &cobra.Command{
		Use:   "status",
		Short: "Check Mini health: connectivity, services, resources",
		RunE: func(cmd *cobra.Command, args []string) error {
			result := &StatusResult{}

			// Ollama
			fmt.Printf("  %s checking ollama...\r", PulseFrame())
			oc := ollamaClient()
			if err := oc.Ping(); err != nil {
				result.Ollama = &ServiceStatus{Running: false, Error: err.Error()}
			} else {
				models, _ := oc.Models()
				result.Ollama = &ServiceStatus{Running: true, Models: len(models)}
			}

			// llama.cpp — short timeout, tunnel to its port
			fmt.Printf("  %s checking llama.cpp...\r", PulseFrame())
			lcURL := cfg.LlamaCPPURL()
			if lt, err := StartTunnel(cfg.SSHUser, cfg.Host, cfg.LlamaCPPPort); err == nil {
				lcURL = lt.LocalURL()
				defer lt.Close()
			}
			lc := NewOllamaClient(lcURL)
			lc.http.Timeout = 3 * time.Second
			if err := lc.Ping(); err != nil {
				result.LlamaCPP = &ServiceStatus{Running: false}
			} else {
				result.LlamaCPP = &ServiceStatus{Running: true}
			}

			// SSH + system info
			fmt.Printf("  %s checking ssh...\r", PulseFrame())
			ssh := sshClient()
			if mem, err := ssh.Run("sysctl -n hw.memsize"); err == nil {
				var memBytes int64
				fmt.Sscanf(mem, "%d", &memBytes)
				totalGB := float64(memBytes) / (1024 * 1024 * 1024)

				fmt.Printf("  %s checking memory...\r", PulseFrame())
				if vs, err := ssh.Run("vm_stat | awk '/Pages active/ {print $3}'"); err == nil {
					var activePages int64
					fmt.Sscanf(vs, "%d", &activePages)
					// macOS ARM64 uses 16KB pages
					usedGB := float64(activePages*16384) / (1024 * 1024 * 1024)
					result.Memory = fmt.Sprintf("%.1f GB / %.1f GB", usedGB, totalGB)
				} else {
					result.Memory = fmt.Sprintf("%.1f GB total", totalGB)
				}
			} else {
				result.SSHError = err.Error()
			}

			if result.SSHError == "" {
				fmt.Printf("  %s checking disk...\r", PulseFrame())
				if df, err := ssh.Run("df -h / | tail -1 | awk '{print $4 \" free\"}'"); err == nil {
					result.Disk = df
				}
				fmt.Printf("  %s checking load...\r", PulseFrame())
				if load, err := ssh.Run("sysctl -n vm.loadavg"); err == nil {
					result.Load = strings.Trim(load, "{ }")
				}
			}
			fmt.Printf("                         \r")

			if jsonOut {
				enc := json.NewEncoder(os.Stdout)
				enc.SetIndent("", "  ")
				return enc.Encode(result)
			}

			// Pretty print
			fmt.Println()
			fmt.Println(Selected.Render("  mini status"))
			fmt.Println(Sep(50))

			if result.Ollama.Running {
				fmt.Println(Row("ollama", StatusOK(
					fmt.Sprintf("running, %d models", result.Ollama.Models))))
			} else {
				fmt.Println(Row("ollama", StatusFail(result.Ollama.Error)))
			}

			if result.LlamaCPP.Running {
				fmt.Println(Row("llama.cpp", StatusOK("running")))
			} else {
				fmt.Println(Row("llama.cpp", StatusInfo("not running")))
			}

			if result.SSHError != "" {
				fmt.Println(Row("ssh", StatusFail(result.SSHError)))
			} else {
				if result.Memory != "" {
					fmt.Println(Row("memory", result.Memory))
				}
				if result.Disk != "" {
					fmt.Println(Row("disk", result.Disk))
				}
				if result.Load != "" {
					fmt.Println(Row("load", result.Load))
				}
			}

			fmt.Println()
			return nil
		},
	}

	cmd.Flags().BoolVar(&jsonOut, "json", false, "output as JSON")
	return cmd
}

// ── mini models ────────────────────────────────────────

func modelsCmd() *cobra.Command {
	var jsonOut bool

	cmd := &cobra.Command{
		Use:   "models",
		Short: "List models on the Mini",
		RunE: func(cmd *cobra.Command, args []string) error {
			models, err := ollamaClient().Models()
			if err != nil {
				return fmt.Errorf("cannot list models: %w", err)
			}

			if jsonOut {
				enc := json.NewEncoder(os.Stdout)
				enc.SetIndent("", "  ")
				return enc.Encode(models)
			}

			if len(models) == 0 {
				fmt.Println(StatusInfo("No models pulled yet"))
				return nil
			}

			sort.Slice(models, func(i, j int) bool {
				return models[i].Size > models[j].Size
			})

			fmt.Println()
			for i, m := range models {
				last := i == len(models)-1
				name := m.Name
				size := FmtSize(m.Size)
				age := shortAge(m.ModifiedAt)

				line := fmt.Sprintf("%-40s %8s  %s", name, size, Info.Render(age))
				if m.Name == cfg.DefaultModel {
					line = Selected.Render(fmt.Sprintf("%-40s %8s  %s", name, size, age))
				}
				fmt.Printf("  %s%s\n", Branch(last), line)
			}
			fmt.Println()
			return nil
		},
	}

	cmd.Flags().BoolVar(&jsonOut, "json", false, "output as JSON")
	return cmd
}

// ── mini ask ───────────────────────────────────────────

func askCmd() *cobra.Command {
	var (
		model     string
		system    string
		temp      float64
		maxTokens int
		noStream  bool
	)

	cmd := &cobra.Command{
		Use:   "ask PROMPT",
		Short: "Send a single-shot prompt to the LLM",
		Long:  "Send a prompt and stream the response. Joins all arguments into one prompt.",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if model == "" {
				model = cfg.DefaultModel
			}

			prompt := strings.Join(args, " ")

			var msgs []ChatMessage
			if system != "" {
				msgs = append(msgs, ChatMessage{Role: "system", Content: system})
			}
			msgs = append(msgs, ChatMessage{Role: "user", Content: prompt})

			req := ChatRequest{
				Model:    model,
				Messages: msgs,
				Options: ChatOptions{
					Temperature: temp,
					NumPredict:  maxTokens,
				},
			}

			oc := ollamaClient()

			if noStream {
				resp, err := oc.Chat(req)
				if err != nil {
					return err
				}
				fmt.Println(resp.Message.Content)
				fmt.Println(Sep(50))
				fmt.Println(Info.Render(fmt.Sprintf("  %s  %d tokens  %s",
					model, resp.EvalCount, FmtTokS(resp.EvalCount, resp.EvalDuration))))
				return nil
			}

			// Streaming (default)
			fmt.Println()
			final, err := oc.ChatStream(req, os.Stdout)
			if err != nil {
				return err
			}
			fmt.Println()
			fmt.Println(Sep(50))
			fmt.Println(Info.Render(fmt.Sprintf("  %s  %d tokens  %s",
				model, final.EvalCount, FmtTokS(final.EvalCount, final.EvalDuration))))
			return nil
		},
	}

	cmd.Flags().StringVarP(&model, "model", "m", "", "model name (default from config)")
	cmd.Flags().StringVarP(&system, "system", "s", "", "system prompt")
	cmd.Flags().Float64VarP(&temp, "temp", "t", 0.3, "temperature")
	cmd.Flags().IntVarP(&maxTokens, "max-tokens", "n", 4096, "max output tokens")
	cmd.Flags().BoolVar(&noStream, "no-stream", false, "wait for full response instead of streaming")

	return cmd
}

// ── mini sessions ──────────────────────────────────────

func sessionsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "sessions",
		Short: "List chat sessions",
		RunE: func(cmd *cobra.Command, args []string) error {
			sessions, err := ListSessions()
			if err != nil {
				return err
			}
			if len(sessions) == 0 {
				fmt.Println(Info.Render("  no sessions yet"))
				return nil
			}

			// Sort by last used (most recent first)
			sort.Slice(sessions, func(i, j int) bool {
				return sessions[i].LastUsed.After(sessions[j].LastUsed)
			})

			fmt.Println()
			for i, s := range sessions {
				last := i == len(sessions)-1
				age := shortAge(s.LastUsed)
				turns := fmt.Sprintf("%d turns", s.Turns)
				if s.Turns == 0 {
					turns = "empty"
				}
				line := fmt.Sprintf("%-30s %8s  %s", s.Name, turns, Info.Render(age))
				fmt.Printf("  %s%s\n", Branch(last), line)
			}
			fmt.Println()
			return nil
		},
	}
}

// ── mini update ────────────────────────────────────────

func updateCmd() *cobra.Command {
	var check, setup bool

	cmd := &cobra.Command{
		Use:   "update",
		Short: "Pull latest code on the Mini",
		RunE: func(cmd *cobra.Command, args []string) error {
			ssh := sshClient()
			repoDir := "~/mini-llm"

			if check {
				// Fetch remote without modifying working tree
				local, err := ssh.Run(fmt.Sprintf("cd %s && git rev-parse --short HEAD", repoDir))
				if err != nil {
					return fmt.Errorf("cannot read version: %w", err)
				}
				ssh.Run(fmt.Sprintf("cd %s && git fetch", repoDir))
				remote, _ := ssh.Run(fmt.Sprintf("cd %s && git rev-parse --short origin/main", repoDir))
				behind, _ := ssh.Run(fmt.Sprintf("cd %s && git rev-list --count HEAD..origin/main", repoDir))

				fmt.Println()
				fmt.Println(Selected.Render("  mini update --check"))
				fmt.Println(Sep(50))
				fmt.Println(Row("local", local))
				fmt.Println(Row("remote", remote))
				if behind == "0" {
					fmt.Println(Row("status", StatusOK("up to date")))
				} else {
					fmt.Println(Row("status", StatusFail(fmt.Sprintf("%s commit(s) behind", behind))))
				}
				fmt.Println()
				return nil
			}

			// Pull
			fmt.Println()
			fmt.Println(Selected.Render("  mini update"))
			fmt.Println(Sep(50))

			output, err := ssh.Run(fmt.Sprintf("cd %s && git pull", repoDir))
			if err != nil {
				return fmt.Errorf("git pull failed: %w", err)
			}
			fmt.Println(Row("pull", output))

			// Show current version
			hash, _ := ssh.Run(fmt.Sprintf("cd %s && git rev-parse --short HEAD", repoDir))
			fmt.Println(Row("version", hash))

			if setup {
				fmt.Println()
				fmt.Println(Info.Render("  running install.sh --yes ..."))
				fmt.Println()
				pathPrefix := "export PATH=$HOME/.local/bin:/opt/homebrew/bin:$PATH"
				if err := ssh.RunInteractive(fmt.Sprintf("%s && cd %s && ./install.sh --yes", pathPrefix, repoDir)); err != nil {
					return fmt.Errorf("install.sh failed: %w", err)
				}
			}

			fmt.Println()
			return nil
		},
	}

	cmd.Flags().BoolVar(&check, "check", false, "compare versions without pulling")
	cmd.Flags().BoolVar(&setup, "setup", false, "rerun install.sh after pulling")
	return cmd
}

// ── mini say ───────────────────────────────────────────

func ttsOnMini(ssh *SSHClient, text string, voice string, speed float64, readable bool) ([]byte, error) {
	ts := time.Now().UnixNano()
	remotePath := fmt.Sprintf("/tmp/mini-tts-%d.mp3", ts)
	repoDir := "~/mini-llm"
	pathPrefix := "export PATH=$HOME/.local/bin:/opt/homebrew/bin:$PATH"

	// Shell-escape text by replacing single quotes
	escaped := strings.ReplaceAll(text, "'", "'\\''")

	ttsDir := repoDir + "/mini-tools/tts"
	uvCmd := fmt.Sprintf("uv run python llmtts.py -t '%s' -o %s --voice %s --speed %.1f",
		escaped, remotePath, voice, speed)
	if readable {
		uvCmd += " --readable"
	}

	fullCmd := fmt.Sprintf("%s && cd %s && %s", pathPrefix, ttsDir, uvCmd)
	// Use RunInteractive so user sees download progress (models, venv setup)
	if err := ssh.RunInteractive(fullCmd); err != nil {
		return nil, fmt.Errorf("TTS generation failed: %w", err)
	}

	// Transfer mp3 bytes and clean up remote file
	data, err := ssh.RunBytes(fmt.Sprintf("cat %s && rm %s", remotePath, remotePath))
	if err != nil {
		return nil, fmt.Errorf("mp3 transfer failed: %w", err)
	}
	return data, nil
}

func playMP3(data []byte) error {
	tmp, err := os.CreateTemp("", "mini-say-*.mp3")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	tmp.Close()

	cmd := exec.Command("ffplay", "-nodisp", "-autoexit", "-loglevel", "quiet", tmp.Name())
	return cmd.Run()
}

func sayCmd() *cobra.Command {
	var (
		ask      bool
		voice    string
		speed    float64
		readable bool
	)

	cmd := &cobra.Command{
		Use:   "say TEXT",
		Short: "Speak text on MacBook speakers (TTS on Mini)",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			text := strings.Join(args, " ")
			ssh := sshClient()

			fmt.Printf("  %s generating speech...\r", PulseFrame())
			data, err := ttsOnMini(ssh, text, voice, speed, readable)
			if err != nil {
				return err
			}
			fmt.Printf("                           \r")

			// 1 MB cap
			if len(data) > 1_000_000 {
				return fmt.Errorf("audio too large (%s), use 'mini tts' for long text", FmtSize(int64(len(data))))
			}

			if err := playMP3(data); err != nil {
				return fmt.Errorf("playback failed: %w", err)
			}

			if ask {
				fmt.Print("> ")
				var input string
				fmt.Scanln(&input)
				fmt.Println(input)
			}

			return nil
		},
	}

	cmd.Flags().BoolVar(&ask, "ask", false, "prompt for input after speaking")
	cmd.Flags().StringVar(&voice, "voice", "af_heart", "TTS voice")
	cmd.Flags().Float64Var(&speed, "speed", 1.0, "speech speed")
	cmd.Flags().BoolVar(&readable, "readable", false, "preprocess numbers/hex for speech")
	return cmd
}

// ── mini tts ───────────────────────────────────────────

func ttsCmd() *cobra.Command {
	var (
		text     string
		output   string
		voice    string
		speed    float64
		readable bool
	)

	cmd := &cobra.Command{
		Use:   "tts [FILE]",
		Short: "Convert text to mp3 (TTS on Mini)",
		RunE: func(cmd *cobra.Command, args []string) error {
			if output == "" {
				return fmt.Errorf("--output / -o is required")
			}

			var inputText string
			if text != "" {
				inputText = text
			} else if len(args) > 0 {
				b, err := os.ReadFile(args[0])
				if err != nil {
					return fmt.Errorf("cannot read %s: %w", args[0], err)
				}
				inputText = string(b)
			} else {
				return fmt.Errorf("either FILE argument or --text is required")
			}

			ssh := sshClient()

			fmt.Printf("  %s generating speech...\r", PulseFrame())
			data, err := ttsOnMini(ssh, inputText, voice, speed, readable)
			if err != nil {
				return err
			}
			fmt.Printf("                           \r")

			if err := os.WriteFile(output, data, 0644); err != nil {
				return fmt.Errorf("cannot write %s: %w", output, err)
			}

			fmt.Println()
			fmt.Println(Selected.Render("  mini tts"))
			fmt.Println(Sep(50))
			fmt.Println(Row("output", output))
			fmt.Println(Row("size", FmtSize(int64(len(data)))))
			fmt.Println()
			return nil
		},
	}

	cmd.Flags().StringVarP(&text, "text", "t", "", "text to speak")
	cmd.Flags().StringVarP(&output, "output", "o", "", "output mp3 file (required)")
	cmd.Flags().StringVar(&voice, "voice", "af_heart", "TTS voice")
	cmd.Flags().Float64Var(&speed, "speed", 1.0, "speech speed")
	cmd.Flags().BoolVar(&readable, "readable", false, "preprocess numbers/hex for speech")
	return cmd
}

// ── helpers ────────────────────────────────────────────

func shortAge(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	case d < 30*24*time.Hour:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	default:
		return fmt.Sprintf("%dmo", int(d.Hours()/(24*30)))
	}
}
