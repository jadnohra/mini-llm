package main

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/spf13/cobra"
)

var daemonStateFile = func() string {
	home, _ := os.UserHomeDir()
	return home + "/.mini/daemon.json"
}()

var daemonLogFile = func() string {
	home, _ := os.UserHomeDir()
	return home + "/.mini/daemon.log"
}()

type DaemonState struct {
	PID      int    `json:"pid"`
	Ollama   int    `json:"ollama"`
	STT      int    `json:"stt"`
	LlamaCPP int    `json:"llamacpp"`
	Started  string `json:"started"`
	Version  string `json:"version"`
	MiniHash string `json:"mini_hash"`
}

func (s *DaemonState) OllamaURL() string {
	return fmt.Sprintf("http://127.0.0.1:%d", s.Ollama)
}

func (s *DaemonState) STTURL() string {
	return fmt.Sprintf("http://127.0.0.1:%d", s.STT)
}

func readDaemonState() (*DaemonState, error) {
	data, err := os.ReadFile(daemonStateFile)
	if err != nil {
		return nil, err
	}
	var state DaemonState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, err
	}
	return &state, nil
}

func writeDaemonState(state *DaemonState) error {
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	tmp := daemonStateFile + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return err
	}
	return os.Rename(tmp, daemonStateFile)
}

func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return proc.Signal(syscall.Signal(0)) == nil
}

// checkVersion warns if the CLI build hash doesn't match the Mini's repo hash.
func checkVersion(state *DaemonState) {
	if version == "dev" || state.MiniHash == "" {
		return
	}
	if version != state.MiniHash {
		fmt.Fprintf(os.Stderr, "  ⚠ version mismatch: mini=%s, Mini=%s — run: mini update\n", version, state.MiniHash)
	}
}

// ensureDaemon returns daemon state, auto-starting the daemon if needed.
func ensureDaemon() (*DaemonState, error) {
	if state, err := readDaemonState(); err == nil && processAlive(state.PID) {
		checkVersion(state)
		return state, nil
	}

	// Start daemon in background
	exe, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("cannot find executable: %w", err)
	}
	cmd := exec.Command(exe, "daemon", "start")
	cmd.Stdout = nil
	cmd.Stderr = nil
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("failed to start daemon: %w", err)
	}

	// Wait for state file
	for i := 0; i < 50; i++ {
		time.Sleep(200 * time.Millisecond)
		if state, err := readDaemonState(); err == nil && processAlive(state.PID) {
			return state, nil
		}
	}
	return nil, fmt.Errorf("daemon failed to start (timeout)")
}

// runDaemon is the main loop for the daemon process.
func runDaemon() error {
	// Open tunnels
	tunnels := map[string]*SSHTunnel{}
	ports := map[string]int{
		"ollama":   cfg.OllamaPort,
		"stt":      8090,
		"llamacpp": cfg.LlamaCPPPort,
	}

	for name, remotePort := range ports {
		t, err := StartTunnel(cfg.SSHUser, cfg.Host, cfg.SSHKey, remotePort)
		if err != nil {
			fmt.Fprintf(os.Stderr, "daemon: tunnel %s failed: %v\n", name, err)
			continue
		}
		tunnels[name] = t
		fmt.Fprintf(os.Stderr, "daemon: %s tunnel 127.0.0.1:%d → %s:%d\n", name, t.localPort, cfg.Host, remotePort)
	}

	if len(tunnels) == 0 {
		return fmt.Errorf("no tunnels connected")
	}

	// Read Mini's commit hash
	ssh := NewSSHClient(cfg.SSHUser, cfg.Host, cfg.SSHKey)
	miniHash, _ := ssh.Run("cd ~/mini-llm && git rev-parse --short HEAD")

	// Write state
	state := &DaemonState{
		PID:      os.Getpid(),
		Started:  time.Now().Format(time.RFC3339),
		Version:  version,
		MiniHash: miniHash,
	}
	if t, ok := tunnels["ollama"]; ok {
		state.Ollama = t.localPort
	}
	if t, ok := tunnels["stt"]; ok {
		state.STT = t.localPort
	}
	if t, ok := tunnels["llamacpp"]; ok {
		state.LlamaCPP = t.localPort
	}
	if err := writeDaemonState(state); err != nil {
		return fmt.Errorf("writing state: %w", err)
	}

	fmt.Fprintf(os.Stderr, "daemon: running (pid %d)\n", os.Getpid())

	// Handle shutdown
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)

	// Health check loop — reconnect dead tunnels
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-sigCh:
			fmt.Fprintf(os.Stderr, "daemon: shutting down\n")
			for _, t := range tunnels {
				t.Close()
			}
			os.Remove(daemonStateFile)
			return nil

		case <-ticker.C:
			for name, t := range tunnels {
				conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", t.localPort), 2*time.Second)
				if err == nil {
					conn.Close()
					continue
				}
				// Tunnel dead — reconnect
				fmt.Fprintf(os.Stderr, "daemon: %s tunnel down, reconnecting...\n", name)
				t.Close()
				newT, err := StartTunnel(cfg.SSHUser, cfg.Host, cfg.SSHKey, ports[name])
				if err != nil {
					fmt.Fprintf(os.Stderr, "daemon: %s reconnect failed: %v\n", name, err)
					continue
				}
				tunnels[name] = newT
				fmt.Fprintf(os.Stderr, "daemon: %s reconnected on port %d\n", name, newT.localPort)

				// Update state file with new port
				switch name {
				case "ollama":
					state.Ollama = newT.localPort
				case "stt":
					state.STT = newT.localPort
				case "llamacpp":
					state.LlamaCPP = newT.localPort
				}
				writeDaemonState(state)
			}
		}
	}
}

func daemonCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "daemon [start|stop|status]",
		Short: "Persistent tunnel manager (auto-started by other commands)",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			action := "status"
			if len(args) > 0 {
				action = args[0]
			}

			switch action {
			case "start":
				// Check if already running
				if state, err := readDaemonState(); err == nil && processAlive(state.PID) {
					fmt.Printf("daemon already running (pid %d)\n", state.PID)
					return nil
				}

				// If parent is a terminal, fork to background
				if os.Getenv("MINI_DAEMON") != "1" {
					exe, _ := os.Executable()
					c := exec.Command(exe, "daemon", "start")
					c.Env = append(os.Environ(), "MINI_DAEMON=1")
					c.Stdout = nil
					logF, _ := os.OpenFile(daemonLogFile, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
					c.Stderr = logF
					c.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
					if err := c.Start(); err != nil {
						return fmt.Errorf("failed to fork daemon: %w", err)
					}
					// Wait for it to be ready
					for i := 0; i < 50; i++ {
						time.Sleep(200 * time.Millisecond)
						if state, err := readDaemonState(); err == nil && processAlive(state.PID) {
							fmt.Printf("daemon started (pid %d)\n", state.PID)
							return nil
						}
					}
					return fmt.Errorf("daemon failed to start")
				}

				// We are the forked daemon process
				return runDaemon()

			case "stop":
				state, err := readDaemonState()
				if err != nil || !processAlive(state.PID) {
					fmt.Println("daemon is not running")
					return nil
				}
				proc, _ := os.FindProcess(state.PID)
				proc.Signal(syscall.SIGTERM)
				fmt.Printf("daemon stopped (pid %d)\n", state.PID)
				return nil

			case "status":
				state, err := readDaemonState()
				if err != nil || !processAlive(state.PID) {
					fmt.Println("daemon is not running")
					return nil
				}

				started, _ := time.Parse(time.RFC3339, state.Started)
				uptime := time.Since(started).Truncate(time.Second)

				fmt.Println()
				fmt.Println(Selected.Render("  mini daemon"))
				fmt.Println(Sep(50))
				fmt.Println(Row("pid", strconv.Itoa(state.PID)))
				fmt.Println(Row("uptime", uptime.String()))
				if state.Version != "" || state.MiniHash != "" {
					vStr := state.Version
					if state.MiniHash != "" && state.MiniHash != state.Version {
						vStr += fmt.Sprintf(" (Mini: %s)", state.MiniHash)
					}
					fmt.Println(Row("version", vStr))
				}
				if state.Ollama > 0 {
					status := checkTunnel(state.Ollama)
					fmt.Println(Row("ollama", fmt.Sprintf(":%d %s", state.Ollama, status)))
				}
				if state.STT > 0 {
					status := checkTunnel(state.STT)
					fmt.Println(Row("stt", fmt.Sprintf(":%d %s", state.STT, status)))
				}
				if state.LlamaCPP > 0 {
					status := checkTunnel(state.LlamaCPP)
					fmt.Println(Row("llamacpp", fmt.Sprintf(":%d %s", state.LlamaCPP, status)))
				}
				fmt.Println()
				return nil

			default:
				return fmt.Errorf("unknown action: %s (use start, stop, status)", action)
			}
		},
	}
	return cmd
}

func checkTunnel(port int) string {
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 2*time.Second)
	if err != nil {
		return StatusFail("down")
	}
	conn.Close()
	return StatusOK("up")
}

// ollamaFromDaemon returns an OllamaClient using the daemon's tunnel.
func ollamaFromDaemon() *OllamaClient {
	state, err := ensureDaemon()
	if err != nil {
		// Fallback to direct
		return NewOllamaClient(cfg.OllamaURL())
	}
	return NewOllamaClient(state.OllamaURL())
}

// sttURLFromDaemon returns the STT server URL via the daemon's tunnel.
func sttURLFromDaemon() (string, error) {
	state, err := ensureDaemon()
	if err != nil {
		return "", err
	}
	if state.STT == 0 {
		return "", fmt.Errorf("STT tunnel not available")
	}
	return state.STTURL(), nil
}

// sshFromDaemon returns an SSHClient (still uses SSH mux, not tunnels).
func sshFromDaemon() *SSHClient {
	// Ensure daemon is running (warms up SSH mux socket as side effect)
	ensureDaemon()
	return NewSSHClient(cfg.SSHUser, cfg.Host, cfg.SSHKey)
}

