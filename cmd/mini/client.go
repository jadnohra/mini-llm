package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"
)

// ── Ollama types ───────────────────────────────────────

type OllamaModel struct {
	Name       string    `json:"name"`
	Size       int64     `json:"size"`
	ModifiedAt time.Time `json:"modified_at"`
	Digest     string    `json:"digest"`
}

type OllamaTagsResp struct {
	Models []OllamaModel `json:"models"`
}

type ChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type ChatRequest struct {
	Model    string        `json:"model"`
	Messages []ChatMessage `json:"messages"`
	Stream   bool          `json:"stream"`
	Options  ChatOptions   `json:"options,omitempty"`
}

type ChatOptions struct {
	Temperature   float64 `json:"temperature,omitempty"`
	NumPredict    int     `json:"num_predict,omitempty"`
	TopP          float64 `json:"top_p,omitempty"`
	TopK          int     `json:"top_k,omitempty"`
	RepeatPenalty float64 `json:"repeat_penalty,omitempty"`
}

type ChatResponse struct {
	Message            ChatMessage `json:"message"`
	TotalDuration      int64       `json:"total_duration"`
	LoadDuration       int64       `json:"load_duration"`
	PromptEvalCount    int         `json:"prompt_eval_count"`
	PromptEvalDuration int64       `json:"prompt_eval_duration"`
	EvalCount          int         `json:"eval_count"`
	EvalDuration       int64       `json:"eval_duration"`
}

// Stream chunk for streaming responses
type ChatStreamChunk struct {
	Message ChatMessage `json:"message"`
	Done    bool        `json:"done"`
	// Final chunk includes stats
	TotalDuration      int64 `json:"total_duration,omitempty"`
	PromptEvalCount    int   `json:"prompt_eval_count,omitempty"`
	PromptEvalDuration int64 `json:"prompt_eval_duration,omitempty"`
	EvalCount          int   `json:"eval_count,omitempty"`
	EvalDuration       int64 `json:"eval_duration,omitempty"`
}

// ── Ollama client ──────────────────────────────────────

type OllamaClient struct {
	baseURL string
	http    *http.Client
}

func NewOllamaClient(baseURL string) *OllamaClient {
	// Force IPv4 — .local mDNS often resolves to IPv6 link-local which hangs
	transport := &http.Transport{
		DialContext: (&net.Dialer{
			Timeout: 5 * time.Second,
		}).DialContext,
		ForceAttemptHTTP2: false,
	}
	// Override the dialer to use tcp4 only
	transport.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		d := &net.Dialer{Timeout: 5 * time.Second}
		return d.DialContext(ctx, "tcp4", addr)
	}
	return &OllamaClient{
		baseURL: baseURL,
		http:    &http.Client{Timeout: 10 * time.Minute, Transport: transport},
	}
}

func (c *OllamaClient) Ping() error {
	resp, err := c.http.Get(c.baseURL + "/api/tags")
	if err != nil {
		return fmt.Errorf("cannot reach %s: %w", c.baseURL, err)
	}
	resp.Body.Close()
	return nil
}

func (c *OllamaClient) Models() ([]OllamaModel, error) {
	resp, err := c.http.Get(c.baseURL + "/api/tags")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var tags OllamaTagsResp
	if err := json.NewDecoder(resp.Body).Decode(&tags); err != nil {
		return nil, err
	}
	return tags.Models, nil
}

func (c *OllamaClient) Chat(req ChatRequest) (*ChatResponse, error) {
	req.Stream = false
	body, _ := json.Marshal(req)

	resp, err := c.http.Post(c.baseURL+"/api/chat", "application/json", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("ollama %d: %s", resp.StatusCode, string(b))
	}

	var cr ChatResponse
	if err := json.NewDecoder(resp.Body).Decode(&cr); err != nil {
		return nil, err
	}
	return &cr, nil
}

// ChatStream sends a request and streams tokens to the writer
func (c *OllamaClient) ChatStream(req ChatRequest, w io.Writer) (*ChatStreamChunk, error) {
	req.Stream = true
	body, _ := json.Marshal(req)

	resp, err := c.http.Post(c.baseURL+"/api/chat", "application/json", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("ollama %d: %s", resp.StatusCode, string(b))
	}

	dec := json.NewDecoder(resp.Body)
	var final ChatStreamChunk

	for {
		var chunk ChatStreamChunk
		if err := dec.Decode(&chunk); err != nil {
			if err == io.EOF {
				break
			}
			return nil, err
		}
		if chunk.Message.Content != "" {
			fmt.Fprint(w, chunk.Message.Content)
		}
		if chunk.Done {
			final = chunk
			break
		}
	}

	return &final, nil
}

func (c *OllamaClient) Pull(model string) error {
	body, _ := json.Marshal(map[string]any{"name": model, "stream": false})
	resp, err := c.http.Post(c.baseURL+"/api/pull", "application/json", bytes.NewReader(body))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("pull failed: %s", string(b))
	}
	return nil
}

func (c *OllamaClient) Delete(model string) error {
	body, _ := json.Marshal(map[string]string{"name": model})
	req, _ := http.NewRequest("DELETE", c.baseURL+"/api/delete", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("delete failed: status %d", resp.StatusCode)
	}
	return nil
}

// ── SSH common ──────────────────────────────────────────

// sshCmd builds an ssh exec.Cmd with multiplexing and any extra flags.
// The first connection authenticates and becomes the ControlMaster; all
// subsequent connections (from any process, terminal, or agent) reuse it.
func sshCmd(user, host string, extra []string, command ...string) *exec.Cmd {
	args := []string{
		"-o", "ConnectTimeout=5",
		"-o", "StrictHostKeyChecking=no",
		"-o", "ControlMaster=auto",
		"-o", "ControlPath=~/.ssh/mini-mux-%r@%h:%p",
		"-o", "ControlPersist=10m",
	}
	args = append(args, extra...)
	args = append(args, fmt.Sprintf("%s@%s", user, host))
	args = append(args, command...)
	return exec.Command("ssh", args...)
}

// ── SSH tunnel ─────────────────────────────────────────

type SSHTunnel struct {
	localPort int
	cmd       *exec.Cmd
}

// StartTunnel opens ssh -L localPort:localhost:remotePort user@host -N
func StartTunnel(user, host string, remotePort int) (*SSHTunnel, error) {
	// Find a free local port
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("no free port: %w", err)
	}
	localPort := ln.Addr().(*net.TCPAddr).Port
	ln.Close()

	forward := fmt.Sprintf("%d:localhost:%d", localPort, remotePort)
	cmd := sshCmd(user, host, []string{
		"-o", "ExitOnForwardFailure=yes",
		"-N", "-L", forward,
	})
	cmd.Stderr = io.Discard

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("ssh tunnel: %w", err)
	}

	// Wait for the tunnel to be ready
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", localPort), 500*time.Millisecond)
		if err == nil {
			conn.Close()
			return &SSHTunnel{localPort: localPort, cmd: cmd}, nil
		}
		time.Sleep(200 * time.Millisecond)
	}

	cmd.Process.Kill()
	return nil, fmt.Errorf("ssh tunnel to %s:%d timed out", host, remotePort)
}

func (t *SSHTunnel) LocalURL() string {
	return fmt.Sprintf("http://127.0.0.1:%d", t.localPort)
}

func (t *SSHTunnel) Close() {
	if t.cmd != nil && t.cmd.Process != nil {
		t.cmd.Process.Kill()
		t.cmd.Wait()
	}
}

// ── SSH client ─────────────────────────────────────────

type SSHClient struct {
	user string
	host string
}

func NewSSHClient(user, host string) *SSHClient {
	return &SSHClient{user: user, host: host}
}

func (s *SSHClient) Run(command string) (string, error) {
	cmd := sshCmd(s.user, s.host, nil, command)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		errMsg := strings.TrimSpace(stderr.String())
		if errMsg == "" {
			errMsg = err.Error()
		}
		return "", fmt.Errorf("%s", errMsg)
	}
	return strings.TrimSpace(stdout.String()), nil
}

func (s *SSHClient) RunInteractive(command string) error {
	cmd := sshCmd(s.user, s.host, []string{"-t"}, command)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func (s *SSHClient) RunBytes(command string) ([]byte, error) {
	cmd := sshCmd(s.user, s.host, nil, command)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		errMsg := strings.TrimSpace(stderr.String())
		if errMsg == "" {
			errMsg = err.Error()
		}
		return nil, fmt.Errorf("%s", errMsg)
	}
	return stdout.Bytes(), nil
}

func (s *SSHClient) ReadFile(path string) (string, error) {
	return s.Run(fmt.Sprintf("cat %s", path))
}

func (s *SSHClient) Push(local, remote string) error {
	target := fmt.Sprintf("%s@%s:%s", s.user, s.host, remote)
	cmd := exec.Command("scp",
		"-o", "ControlMaster=auto",
		"-o", "ControlPath=~/.ssh/mini-mux-%r@%h:%p",
		"-o", "ControlPersist=10m",
		"-r", local, target,
	)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
