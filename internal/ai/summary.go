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

type WrongQuestion struct {
	Stem          string `json:"stem"`
	StudentAnswer string `json:"student_answer"`
	CorrectAnswer string `json:"correct_answer"`
	KnowledgeTag  string `json:"knowledge_tag,omitempty"`
	Explanation   string `json:"explanation,omitempty"`
}

type SummarizeInput struct {
	QuizTitle      string          `json:"quiz_title"`
	ScoreCorrect   int             `json:"score_correct"`
	ScoreTotal     int             `json:"score_total"`
	Strengths      []string        `json:"strengths"`
	Weaknesses     []string        `json:"weaknesses"`
	WrongQuestions []WrongQuestion `json:"wrong_questions"`
}

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

const systemPrompt = `你是一位大学课堂教学助手。学生刚完成一次课堂小测，请根据他的错题信息给出个性化的学习反馈。

要求：
1. 分析学生的掌握情况，指出哪些知识点掌握良好，哪些有问题
2. 对每道错题，解释关键知识点和错误原因
3. 尽量给出一个生活中的例子帮助学生理解错题涉及的概念，格式如：
   "生活中的xxx例子可以帮助你理解这个问题：xxx是xxx样子，它与这道题的关系是xxx。当你下次遇到类似问题，可以想想这个例子。"
4. 给出具体可执行的改进建议

请输出 JSON，包含以下字段：
- strengths: string[] — 掌握较好的知识点（简短）
- weaknesses: string[] — 需要加强的知识点（简短）
- next_actions: string[] — 具体的行动建议（每条包含错题分析、生活类比、改进方法，可以较长）
- priority_level: string — 优先级（"高"/"中"/"低"）
- encouragement: string — 一句鼓励的话

注意：语言要亲切自然，像老师对学生说话一样。不要使用空洞的套话。只输出合法 JSON，不要 markdown 代码块或其他内容。`

func (c *Client) Summarize(ctx context.Context, in SummarizeInput) (domain.ResultSummary, error) {
	c.mu.RLock()
	endpoint := c.endpoint
	apiKey := c.apiKey
	model := c.model
	c.mu.RUnlock()

	if strings.TrimSpace(endpoint) == "" {
		return ruleBased(in), fmt.Errorf("AI endpoint 未配置")
	}

	userMsg, _ := json.Marshal(in)
	url := resolveEndpoint(endpoint)
	payload := map[string]any{
		"model": model,
		"messages": []map[string]string{
			{"role": "system", "content": systemPrompt},
			{"role": "user", "content": string(userMsg)},
		},
		"temperature": 0.7,
	}
	body, _ := json.Marshal(payload)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		c.setLastError(err.Error())
		return ruleBased(in), err
	}
	req.Header.Set("Content-Type", "application/json")
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}

	resp, err := c.httpCli.Do(req)
	if err != nil {
		c.setLastError(err.Error())
		return ruleBased(in), err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		msg := fmt.Sprintf("AI 返回状态码 %d", resp.StatusCode)
		c.setLastError(msg)
		return ruleBased(in), fmt.Errorf("%s", msg)
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
		return ruleBased(in), err
	}
	if len(chatResp.Choices) == 0 {
		msg := "AI 返回空 choices"
		c.setLastError(msg)
		return ruleBased(in), fmt.Errorf("%s", msg)
	}

	content := stripCodeFence(strings.TrimSpace(chatResp.Choices[0].Message.Content))
	var summary domain.ResultSummary
	if err := json.Unmarshal([]byte(content), &summary); err != nil {
		c.setLastError("解析 AI JSON 失败: " + err.Error())
		return ruleBased(in), err
	}

	c.setLastSuccess(time.Now())
	return summary, nil
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

func ruleBased(in SummarizeInput) domain.ResultSummary {
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
