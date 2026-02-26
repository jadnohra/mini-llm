package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

var _ LLMClient = (*HFClient)(nil)

// ── HF Inference API client ──────────────────────────

type HFClient struct {
	baseURL string
	token   string
	http    *http.Client
}

func NewHFClient(token string) *HFClient {
	return &HFClient{
		baseURL: "https://router.huggingface.co/v1",
		token:   token,
		http:    &http.Client{},
	}
}

// ── OpenAI-compatible request/response types ─────────

type hfChatRequest struct {
	Model       string        `json:"model"`
	Messages    []ChatMessage `json:"messages"`
	Stream      bool          `json:"stream"`
	Temperature float64       `json:"temperature,omitempty"`
	MaxTokens   int           `json:"max_tokens,omitempty"`
	TopP        float64       `json:"top_p,omitempty"`
}

type hfChatResponse struct {
	Choices []struct {
		Message ChatMessage `json:"message"`
	} `json:"choices"`
	Usage struct {
		CompletionTokens int `json:"completion_tokens"`
		PromptTokens     int `json:"prompt_tokens"`
	} `json:"usage"`
}

type hfStreamChunk struct {
	Choices []struct {
		Delta struct {
			Content string `json:"content"`
		} `json:"delta"`
	} `json:"choices"`
	Usage *struct {
		CompletionTokens int `json:"completion_tokens"`
	} `json:"usage,omitempty"`
}

// ── Request builder ──────────────────────────────────

func (c *HFClient) buildRequest(req ChatRequest) hfChatRequest {
	return hfChatRequest{
		Model:       req.Model,
		Messages:    req.Messages,
		Stream:      req.Stream,
		Temperature: req.Options.Temperature,
		MaxTokens:   req.Options.NumPredict,
		TopP:        req.Options.TopP,
	}
}

// ── Chat (non-streaming) ────────────────────────────

func (c *HFClient) Chat(req ChatRequest) (*ChatResponse, error) {
	req.Stream = false
	t0 := time.Now()

	hfReq := c.buildRequest(req)
	body, _ := json.Marshal(hfReq)

	httpReq, err := http.NewRequest("POST", c.baseURL+"/chat/completions", strings.NewReader(string(body)))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+c.token)

	resp, err := c.http.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("hf %d: %s", resp.StatusCode, string(b))
	}

	var hfResp hfChatResponse
	if err := json.NewDecoder(resp.Body).Decode(&hfResp); err != nil {
		return nil, fmt.Errorf("hf decode: %w", err)
	}

	if len(hfResp.Choices) == 0 {
		return nil, fmt.Errorf("hf: no choices in response")
	}

	elapsed := time.Since(t0).Nanoseconds()
	evalCount := hfResp.Usage.CompletionTokens

	return &ChatResponse{
		Message:      hfResp.Choices[0].Message,
		EvalCount:    evalCount,
		EvalDuration: elapsed,
	}, nil
}

// ── ChatStream ──────────────────────────────────────

func (c *HFClient) ChatStream(req ChatRequest, w io.Writer) (*ChatStreamChunk, error) {
	return c.ChatStreamCb(context.Background(), req, w, nil)
}

// ── ChatStreamCb (SSE) ──────────────────────────────

func (c *HFClient) ChatStreamCb(ctx context.Context, req ChatRequest, w io.Writer, onFirstToken func()) (*ChatStreamChunk, error) {
	req.Stream = true
	t0 := time.Now()

	hfReq := c.buildRequest(req)
	body, _ := json.Marshal(hfReq)

	httpReq, err := http.NewRequestWithContext(ctx, "POST", c.baseURL+"/chat/completions", strings.NewReader(string(body)))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+c.token)

	resp, err := c.http.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("hf %d: %s", resp.StatusCode, string(b))
	}

	scanner := bufio.NewScanner(resp.Body)
	var fullContent strings.Builder
	tokenCount := 0
	first := true

	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := strings.TrimPrefix(line, "data: ")
		if data == "[DONE]" {
			break
		}

		var chunk hfStreamChunk
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			continue
		}

		if len(chunk.Choices) == 0 {
			continue
		}

		content := chunk.Choices[0].Delta.Content
		if content != "" {
			if first && onFirstToken != nil {
				onFirstToken()
				first = false
			}
			fmt.Fprint(w, content)
			fullContent.WriteString(content)
			tokenCount++
		}

		// Some providers include usage in the last chunk
		if chunk.Usage != nil && chunk.Usage.CompletionTokens > 0 {
			tokenCount = chunk.Usage.CompletionTokens
		}
	}

	if ctx.Err() != nil {
		return nil, ctx.Err()
	}

	elapsed := time.Since(t0).Nanoseconds()

	return &ChatStreamChunk{
		Message:      ChatMessage{Role: "assistant", Content: fullContent.String()},
		Done:         true,
		EvalCount:    tokenCount,
		EvalDuration: elapsed,
	}, nil
}
