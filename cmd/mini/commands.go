package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"
)

func ollamaClient() *OllamaClient { return ollamaFromDaemon() }
func sshClient() *SSHClient       { return sshFromDaemon() }

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

			// llama.cpp — use daemon tunnel
			fmt.Printf("  %s checking llama.cpp...\r", PulseFrame())
			lcURL := cfg.LlamaCPPURL()
			if state, err := ensureDaemon(); err == nil && state.LlamaCPP > 0 {
				lcURL = fmt.Sprintf("http://127.0.0.1:%d", state.LlamaCPP)
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
	if _, err := ssh.Run(fullCmd); err != nil {
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
		Use:   "say [TEXT]",
		Short: "Speak text on MacBook speakers (TTS on Mini)",
		RunE: func(cmd *cobra.Command, args []string) error {
			var text string
			if len(args) > 0 {
				text = strings.Join(args, " ")
			} else {
				fmt.Print(Info.Render("say: "))
				scanner := bufio.NewScanner(os.Stdin)
				if scanner.Scan() {
					text = strings.TrimSpace(scanner.Text())
				}
			}
			if text == "" {
				return fmt.Errorf("no text provided")
			}
			ssh := sshClient()

			stop := Spinner("Generating")
			data, err := ttsOnMini(ssh, text, voice, speed, readable)
			stop()
			if err != nil {
				return err
			}

			// 1 MB cap
			if len(data) > 1_000_000 {
				return fmt.Errorf("audio too large (%s), use 'mini tts' for long text", FmtSize(int64(len(data))))
			}

			stop = Spinner("Speaking")
			if err := playMP3(data); err != nil {
				stop()
				return fmt.Errorf("playback failed: %w", err)
			}
			stop()

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

			stop := Spinner("Generating")
			data, err := ttsOnMini(ssh, inputText, voice, speed, readable)
			stop()
			if err != nil {
				return err
			}

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

// ── mini dictate ───────────────────────────────────────

// toolBin returns the path to a compiled binary in mini-tools/<name>/<name>.
func toolBin(name string) string {
	exe, _ := os.Executable()
	if exe != "" {
		dir := filepath.Dir(exe)
		candidate := filepath.Join(dir, "..", "repos", "jad", "mini-llm", "mini-tools", name, name)
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}
	home, _ := os.UserHomeDir()
	for _, path := range []string{
		filepath.Join(home, "repos", "jad", "mini-llm", "mini-tools", name, name),
		filepath.Join(home, "mini-llm", "mini-tools", name, name),
	} {
		if _, err := os.Stat(path); err == nil {
			return path
		}
	}
	return filepath.Join("mini-tools", name, name)
}

func dictateCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "dictate [on|off]",
		Short: "Menubar dictation (Ctrl+Shift+M to record and paste)",
		Long:  "Launches a persistent menubar app. Press Ctrl+Shift+M or click the icon to record, transcribe, and paste into the focused terminal.",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			action := "on"
			if len(args) > 0 {
				action = args[0]
			}

			pipePath := "/tmp/mini-dictate.pipe"
			pidFile := "/tmp/mini-dictate.pid"

			switch action {
			case "off":
				if data, err := os.ReadFile(pidFile); err == nil {
					pidStr := strings.TrimSpace(string(data))
					if c := exec.Command("kill", "-0", pidStr); c.Run() == nil {
						if f, err := os.OpenFile(pipePath, os.O_WRONLY, 0); err == nil {
							f.WriteString("quit\n")
							f.Close()
							fmt.Println("dictate stopped")
							return nil
						}
					}
				}
				fmt.Println("dictate is not running")
				return nil

			case "on":
				bin := toolBin("dictate")
				if _, err := os.Stat(bin); err != nil {
					return fmt.Errorf("dictate binary not found at %s — compile with:\n  swiftc -O -o %s mini-tools/dictate/dictate.swift -framework AppKit -framework Carbon", bin, bin)
				}

				if data, err := os.ReadFile(pidFile); err == nil {
					pidStr := strings.TrimSpace(string(data))
					if c := exec.Command("kill", "-0", pidStr); c.Run() == nil {
						fmt.Println("dictate is already running")
						return nil
					}
				}

				// Ensure STT server is running (don't kill warm server)
				ssh := sshClient()
				if health, err := ssh.Run("curl -sf --max-time 2 http://localhost:8090/health"); err != nil || health == "" {
					sttStartServer(ssh)
				}

				// Get STT URL from daemon (tunnel is persistent)
				sttBase, err := sttURLFromDaemon()
				if err != nil {
					return fmt.Errorf("cannot get STT tunnel: %w", err)
				}
				sttURL := sttBase + "/transcribe"

				c := exec.Command(bin, "--stt-url", sttURL)
				c.Stdout = nil
				home, _ := os.UserHomeDir()
				logF, _ := os.OpenFile(home+"/.mini/dictate.log", os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
				c.Stderr = logF
				c.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
				if err := c.Start(); err != nil {
					return fmt.Errorf("failed to start dictate: %w", err)
				}
				fmt.Printf("dictate started (pid %d) — Ctrl+Shift+M or click [m] in menubar\n", c.Process.Pid)
				return nil

			default:
				return fmt.Errorf("unknown action: %s (use 'on' or 'off')", action)
			}
		},
	}
	return cmd
}

// ── mini hear ──────────────────────────────────────────

// recorderBin returns the path to the native recorder binary.
func recorderBin() string {
	return toolBin("recorder")
}

// recordAudio launches the native recorder window. Returns the path to the
// recorded audio file. Works from any context: terminal, agent, tool call.
func recordAudio() (string, error) {
	wavPath := fmt.Sprintf("/tmp/mini-stt-%d.m4a", time.Now().UnixNano())

	cmd := exec.Command(recorderBin(), "--output", wavPath, "--position", "center")
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		os.Remove(wavPath)
		return "", fmt.Errorf("recording cancelled")
	}

	// Verify we got audio
	info, err := os.Stat(wavPath)
	if err != nil || info.Size() < 1000 {
		os.Remove(wavPath)
		return "", fmt.Errorf("no audio recorded")
	}

	return wavPath, nil
}

// sttOnMini sends audio to the Mini's STT server via SSH tunnel and returns text.
// Uses a persistent STT server on the Mini (port 8090). If the server isn't
// running, starts it automatically via nohup. Posts audio directly over HTTP
// tunnel — no SCP needed.
func sttOnMini(ssh *SSHClient, localPath string, model string, lang string) (string, error) {
	// Read audio file
	audioData, err := os.ReadFile(localPath)
	if err != nil {
		return "", fmt.Errorf("reading audio: %w", err)
	}

	// Try posting directly via tunnel
	t0 := time.Now()
	text, err := sttPost(audioData, lang)
	if err == nil {
		fmt.Fprintf(os.Stderr, "  transcribe: %dms\n", time.Since(t0).Milliseconds())
		return text, nil
	}
	fmt.Fprintf(os.Stderr, "  server not running, starting...\n")

	// Server not running — start it, then retry
	t0 = time.Now()
	if startErr := sttStartServer(ssh); startErr != nil {
		fmt.Fprintf(os.Stderr, "  server start failed: %v\n", startErr)
		return "", fmt.Errorf("stt server failed to start: %w", startErr)
	}
	fmt.Fprintf(os.Stderr, "  server start: %dms\n", time.Since(t0).Milliseconds())

	t0 = time.Now()
	text, err = sttPost(audioData, lang)
	fmt.Fprintf(os.Stderr, "  transcribe: %dms\n", time.Since(t0).Milliseconds())
	if err != nil {
		return "", fmt.Errorf("transcription failed: %w", err)
	}
	return text, nil
}

// sttPost sends audio data directly to the STT server via daemon tunnel.
func sttPost(audioData []byte, lang string) (string, error) {
	t0 := time.Now()
	sttBase, err := sttURLFromDaemon()
	if err != nil {
		return "", err
	}
	fmt.Fprintf(os.Stderr, "    daemon lookup: %dms\n", time.Since(t0).Milliseconds())

	req, _ := http.NewRequest("POST", sttBase+"/transcribe", bytes.NewReader(audioData))
	req.Header.Set("Content-Type", "audio/m4a")
	req.Header.Set("X-Language", lang)

	t1 := time.Now()
	client := &http.Client{Timeout: 2 * time.Minute}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	fmt.Fprintf(os.Stderr, "    http post: %dms (%d bytes)\n", time.Since(t1).Milliseconds(), len(audioData))

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("stt server %d: %s", resp.StatusCode, string(body))
	}
	return strings.TrimSpace(string(body)), nil
}

func sttStartServer(ssh *SSHClient) error {
	pathPrefix := "export PATH=$HOME/.local/bin:/opt/homebrew/bin:$PATH"
	sttDir := "~/mini-llm/mini-tools/ts-align"
	// Kill any existing server to avoid port conflict
	ssh.Run("pkill -f stt_server.py 2>/dev/null")
	time.Sleep(500 * time.Millisecond)
	// Ensure venv exists with deps, then start server in background
	startCmd := fmt.Sprintf(`%s && cd %s && `+
		`(test -f $HOME/.mini/envs/stt/bin/python || (uv venv --python 3.12 $HOME/.mini/envs/stt && uv pip install --python $HOME/.mini/envs/stt/bin/python mlx-whisper numpy)) && `+
		`nohup $HOME/.mini/envs/stt/bin/python stt_server.py --port 8090 --idle-timeout 3600 </dev/null >>$HOME/Library/Logs/stt-server.log 2>>$HOME/Library/Logs/stt-server.err &`,
		pathPrefix, sttDir)
	if _, err := ssh.Run(startCmd); err != nil {
		return err
	}
	// Wait for server to be ready (up to 30s — first start may download model)
	for i := 0; i < 30; i++ {
		time.Sleep(time.Second)
		if health, err := ssh.Run("curl -sf http://localhost:8090/health"); err == nil && health != "" {
			return nil
		}
	}
	return fmt.Errorf("stt server did not start")
}

func sttCmd() *cobra.Command {
	var (
		copyClip bool
		model    string
		lang     string
		filePath string
	)

	cmd := &cobra.Command{
		Use:     "stt",
		Aliases: []string{"hear"},
		Short:   "Record voice and transcribe (STT on Mini)",
		Long:    "Record from MacBook mic, send to Mini for speech-to-text via MLX Whisper.",
		RunE: func(cmd *cobra.Command, args []string) error {
			var audioPath string
			var cleanup bool

			if filePath != "" {
				// Use provided file directly
				audioPath = filePath
			} else {
				// Record from mic
				stop := Spinner("Recording")
				var err error
				audioPath, err = recordAudio()
				stop()
				if err != nil {
					return err
				}
				cleanup = true
			}
			if cleanup {
				defer os.Remove(audioPath)
			}

			// Transcribe on Mini
			ssh := sshClient()
			stop := Spinner("Transcribing")
			text, err := sttOnMini(ssh, audioPath, model, lang)
			stop()
			if err != nil {
				return err
			}

			// Output
			fmt.Println(text)

			if copyClip {
				clip := exec.Command("pbcopy")
				clip.Stdin = strings.NewReader(text)
				clip.Run()
			}

			return nil
		},
	}

	cmd.Flags().BoolVar(&copyClip, "copy", false, "copy transcribed text to clipboard")
	cmd.Flags().StringVarP(&model, "model", "m", "mlx-community/whisper-large-v3-turbo", "Whisper model")
	cmd.Flags().StringVarP(&lang, "lang", "l", "en", "language code")
	cmd.Flags().StringVarP(&filePath, "file", "f", "", "transcribe existing audio file (skip recording)")
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
