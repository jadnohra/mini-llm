package main

import (
	"bufio"
	"encoding/json"
	"strings"
	"testing"
)

func TestParseProvider(t *testing.T) {
	tests := []struct {
		input     string
		wantProv  string
		wantModel string
	}{
		{"qwen2.5-coder:32b", "ollama", "qwen2.5-coder:32b"},
		{"ollama/qwen2.5-coder:32b", "ollama", "qwen2.5-coder:32b"},
		{"hf/Qwen/Qwen2.5-Coder-32B-Instruct", "hf", "Qwen/Qwen2.5-Coder-32B-Instruct"},
		{"hf/meta-llama/Llama-3-8B-Instruct:together", "hf", "meta-llama/Llama-3-8B-Instruct:together"},
		{"llama3.2:3b", "ollama", "llama3.2:3b"},
		// Unknown prefix treated as ollama model
		{"myorg/mymodel", "ollama", "myorg/mymodel"},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			prov, model := parseProvider(tt.input)
			if prov != tt.wantProv || model != tt.wantModel {
				t.Errorf("parseProvider(%q) = (%q, %q), want (%q, %q)",
					tt.input, prov, model, tt.wantProv, tt.wantModel)
			}
		})
	}
}

func TestHFSSEParsing(t *testing.T) {
	// Simulate SSE stream from HF
	sse := strings.Join([]string{
		"data: {\"choices\":[{\"delta\":{\"content\":\"Hello\"}}]}",
		"",
		"data: {\"choices\":[{\"delta\":{\"content\":\" world\"}}]}",
		"",
		"data: {\"choices\":[{\"delta\":{\"content\":\"!\"}}],\"usage\":{\"completion_tokens\":3}}",
		"",
		"data: [DONE]",
		"",
	}, "\n")

	scanner := bufio.NewScanner(strings.NewReader(sse))
	var content strings.Builder
	tokenCount := 0

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
			t.Fatalf("unmarshal chunk: %v", err)
		}
		if len(chunk.Choices) > 0 {
			content.WriteString(chunk.Choices[0].Delta.Content)
			tokenCount++
		}
		if chunk.Usage != nil && chunk.Usage.CompletionTokens > 0 {
			tokenCount = chunk.Usage.CompletionTokens
		}
	}

	if content.String() != "Hello world!" {
		t.Errorf("content = %q, want %q", content.String(), "Hello world!")
	}
	if tokenCount != 3 {
		t.Errorf("tokenCount = %d, want 3", tokenCount)
	}
}

func TestHFRequestMapping(t *testing.T) {
	client := NewHFClient("test-token")
	req := ChatRequest{
		Model: "Qwen/Qwen2.5-Coder-32B-Instruct",
		Messages: []ChatMessage{
			{Role: "user", Content: "hello"},
		},
		Options: ChatOptions{
			Temperature:   0.7,
			NumPredict:    1024,
			TopP:          0.9,
			TopK:          40,           // should be dropped
			RepeatPenalty: 1.1,          // should be dropped
		},
	}

	hfReq := client.buildRequest(req)

	if hfReq.Model != "Qwen/Qwen2.5-Coder-32B-Instruct" {
		t.Errorf("model = %q", hfReq.Model)
	}
	if hfReq.Temperature != 0.7 {
		t.Errorf("temperature = %f", hfReq.Temperature)
	}
	if hfReq.MaxTokens != 1024 {
		t.Errorf("max_tokens = %d", hfReq.MaxTokens)
	}
	if hfReq.TopP != 0.9 {
		t.Errorf("top_p = %f", hfReq.TopP)
	}

	// Verify JSON doesn't contain ollama-specific fields
	data, _ := json.Marshal(hfReq)
	s := string(data)
	if strings.Contains(s, "top_k") {
		t.Error("JSON should not contain top_k")
	}
	if strings.Contains(s, "repeat_penalty") {
		t.Error("JSON should not contain repeat_penalty")
	}
	if strings.Contains(s, "num_predict") {
		t.Error("JSON should not contain num_predict")
	}
}
