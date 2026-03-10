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

	"course-assistant/internal/domain"
)

type Input struct {
	ScoreCorrect int      `json:"score_correct"`
	ScoreTotal   int      `json:"score_total"`
	Strengths    []string `json:"strengths"`
	Weaknesses   []string `json:"weaknesses"`
}

type Client struct {
	Endpoint string
	APIKey   string
	Model    string
	HTTP     *http.Client
	mu       sync.RWMutex
	lastOK   time.Time
	lastErr  string
}

func NewClient(endpoint, apiKey, model string) *Client {
	return &Client{
		Endpoint: endpoint,
		APIKey:   apiKey,
		Model:    model,
		HTTP:     &http.Client{Timeout: 15 * time.Second},
	}
}

func (c *Client) Summarize(ctx context.Context, in Input) domain.ResultSummary {
	base := ruleBased(in)
	if strings.TrimSpace(c.Endpoint) == "" {
		return base
	}
	payload := map[string]any{
		"model": c.Model,
		"input": map[string]any{
			"instruction": "请输出 JSON，字段: strengths, weaknesses, next_actions, priority_level, encouragement。内容要给学生可执行建议，中文简洁。",
			"data":        in,
		},
	}
	body, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.Endpoint, bytes.NewReader(body))
	if err != nil {
		c.setLastError(err.Error())
		return base
	}
	req.Header.Set("Content-Type", "application/json")
	if c.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.APIKey)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		c.setLastError(err.Error())
		return base
	}
	defer resp.Body.Close()
	var out struct {
		Output domain.ResultSummary `json:"output"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		c.setLastError(err.Error())
		return base
	}
	if len(out.Output.NextActions) == 0 {
		c.setLastError("output.next_actions empty")
		return base
	}
	c.setLastSuccess(time.Now())
	return out.Output
}

func (c *Client) Health() map[string]any {
	c.mu.RLock()
	lastOK := c.lastOK
	lastErr := c.lastErr
	c.mu.RUnlock()
	lastSuccessAt := ""
	if !lastOK.IsZero() {
		lastSuccessAt = lastOK.Format(time.RFC3339)
	}
	return map[string]any{
		"endpoint":        c.Endpoint,
		"model":           c.Model,
		"key_loaded":      strings.TrimSpace(c.APIKey) != "",
		"last_success_at": lastSuccessAt,
		"last_error":      lastErr,
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

func ruleBased(in Input) domain.ResultSummary {
	ratio := 0.0
	if in.ScoreTotal > 0 {
		ratio = float64(in.ScoreCorrect) / float64(in.ScoreTotal)
	}
	priority := "中"
	if ratio < 0.6 {
		priority = "高"
	}
	if ratio >= 0.85 {
		priority = "低"
	}
	actions := []string{
		"先复盘错题，记录每题错因并归类。",
		"针对薄弱知识点做 2-3 道同类型练习。",
		"下一次课前用 15 分钟快速回顾本次重点。",
	}
	if len(in.Weaknesses) > 0 {
		actions[1] = fmt.Sprintf("优先复习：%s，并完成同类练习。", strings.Join(in.Weaknesses, "、"))
	}
	return domain.ResultSummary{
		Strengths:     in.Strengths,
		Weaknesses:    in.Weaknesses,
		NextActions:   actions,
		Priority:      priority,
		Encouragement: "你已经在持续进步，按建议复盘一轮会更稳。",
	}
}
