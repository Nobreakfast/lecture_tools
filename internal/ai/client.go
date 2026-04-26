// Copyright 2024-2026 course-assistant contributors.
// SPDX-License-Identifier: MIT

package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"
)

type Client struct {
	endpoint string
	apiKey   string
	model    string
	httpCli  *http.Client
	mu       sync.RWMutex
	lastOK   time.Time
	lastErr  string
}

func NewClient(endpoint, apiKey, model string) *Client {
	return &Client{
		endpoint: strings.TrimSpace(endpoint),
		apiKey:   strings.TrimSpace(apiKey),
		model:    strings.TrimSpace(model),
		httpCli:  &http.Client{Timeout: 60 * time.Second},
	}
}

func (c *Client) UpdateConfig(endpoint, apiKey, model string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if endpoint != "" {
		c.endpoint = strings.TrimSpace(endpoint)
	}
	if apiKey != "" {
		c.apiKey = strings.TrimSpace(apiKey)
	}
	if model != "" {
		c.model = strings.TrimSpace(model)
	}
}

func (c *Client) Health() map[string]any {
	c.mu.RLock()
	ep := c.endpoint
	m := c.model
	k := c.apiKey
	ok := c.lastOK
	le := c.lastErr
	c.mu.RUnlock()
	las := ""
	if !ok.IsZero() {
		las = ok.Format(time.RFC3339)
	}
	return map[string]any{
		"endpoint":        ep,
		"model":           m,
		"key_loaded":      strings.TrimSpace(k) != "",
		"last_success_at": las,
		"last_error":      le,
	}
}

func (c *Client) setLastError(msg string) {
	c.mu.Lock()
	c.lastErr = msg
	c.mu.Unlock()
}

func (c *Client) setLastSuccess(t time.Time) {
	c.mu.Lock()
	c.lastOK = t
	c.lastErr = ""
	c.mu.Unlock()
}

// chat sends a chat-completion request to the configured AI endpoint and
// returns the stripped content string. All four public methods (Summarize,
// AdminSummarize, HistorySummarize, GenerateQuiz/AutoFillQuiz) delegate to
// this single method, eliminating the duplicated HTTP / JSON / error-handling
// boilerplate across the package.
func (c *Client) chat(ctx context.Context, systemPrompt, userMsg string, temperature float64) (string, error) {
	c.mu.RLock()
	endpoint := c.endpoint
	apiKey := c.apiKey
	model := c.model
	c.mu.RUnlock()

	if strings.TrimSpace(endpoint) == "" {
		return "", fmt.Errorf("AI endpoint 未配置")
	}

	url := resolveEndpoint(endpoint)
	payload := map[string]any{
		"model": model,
		"messages": []map[string]string{
			{"role": "system", "content": systemPrompt},
			{"role": "user", "content": userMsg},
		},
		"temperature": temperature,
	}
	body, _ := json.Marshal(payload)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		c.setLastError(err.Error())
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}

	resp, err := c.httpCli.Do(req)
	if err != nil {
		c.setLastError(err.Error())
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		msg := fmt.Sprintf("AI 返回状态码 %d", resp.StatusCode)
		c.setLastError(msg)
		return "", fmt.Errorf("%s", msg)
	}

	var chatResp struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&chatResp); err != nil {
		c.setLastError(err.Error())
		return "", err
	}
	if len(chatResp.Choices) == 0 {
		msg := "AI 返回空 choices"
		c.setLastError(msg)
		return "", fmt.Errorf("%s", msg)
	}

	content := stripCodeFence(strings.TrimSpace(chatResp.Choices[0].Message.Content))
	c.setLastSuccess(time.Now())
	return content, nil
}

func resolveEndpoint(endpoint string) string {
	endpoint = strings.TrimRight(endpoint, "/")
	if strings.HasSuffix(endpoint, "/chat/completions") {
		return endpoint
	}
	if strings.HasSuffix(endpoint, "/v1") {
		return endpoint + "/chat/completions"
	}
	return endpoint + "/v1/chat/completions"
}

func stripCodeFence(s string) string {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "```") {
		lines := strings.SplitN(s, "\n", 2)
		if len(lines) == 2 {
			s = lines[1]
		}
		if idx := strings.LastIndex(s, "```"); idx >= 0 {
			s = s[:idx]
		}
	}
	return strings.TrimSpace(s)
}