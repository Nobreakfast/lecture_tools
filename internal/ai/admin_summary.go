package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

type AdminQuestionStat struct {
	QuestionID         string         `json:"question_id"`
	Stem               string         `json:"stem"`
	KnowledgeTag       string         `json:"knowledge_tag,omitempty"`
	CorrectCount       int            `json:"correct_count"`
	AnsweredCount      int            `json:"answered_count"`
	CorrectRate        float64        `json:"correct_rate"`
	AnswerDistribution map[string]int `json:"answer_distribution,omitempty"`
	CommonWrongAnswers []string       `json:"common_wrong_answers,omitempty"`
	Explanation        string         `json:"explanation,omitempty"`
}

type AdminFeedbackItem struct {
	QuestionID   string         `json:"question_id"`
	Stem         string         `json:"stem"`
	Type         string         `json:"type"`
	OptionCounts map[string]int `json:"option_counts,omitempty"`
	TextSamples  []string       `json:"text_samples,omitempty"`
}

type AdminSummarizeInput struct {
	QuizID        string              `json:"quiz_id"`
	QuizTitle     string              `json:"quiz_title"`
	StudentCount  int                 `json:"student_count"`
	AvgScore      float64             `json:"avg_score"`
	QuestionStats []AdminQuestionStat `json:"question_stats"`
	FeedbackItems []AdminFeedbackItem `json:"feedback_items"`
	PDFContext    string              `json:"pdf_context,omitempty"`
}

type AdminSummary struct {
	AnswerAnalysis      string `json:"answer_analysis"`
	FeedbackSummary     string `json:"feedback_summary"`
	TeachingSuggestions string `json:"teaching_suggestions"`
}

const adminSystemPrompt = `你是一位大学课堂教学分析助手。教师刚完成一次课堂小测，请根据全班学生的答题统计数据和反馈，为教师生成一份教学总结报告。

要求分为以下三个部分：

1. **答题情况与错题总结**（answer_analysis）：
   - 概述全班整体答题表现（平均分、答题人数）
   - 指出正确率较低的题目，分析学生常见错误和可能的知识薄弱点
   - 如：大部分题目回答良好，但第X题错误率高达XX%，说明学生对于XXX知识的掌握不够牢靠

2. **学生反馈总结**（feedback_summary）：
   - 汇总学生在问卷和简答题中的反馈意见
   - 提炼学生对教学方式、内容难度、教学节奏等方面的共同看法
   - 如：学生反馈提到教师讲课XXX、XXX知识比较抽象、希望XXX

3. **教学建议**（teaching_suggestions）：
   - 基于答题数据和学生反馈，给出对下节课的具体建议
   - 包括：需要复习的知识点、需要调整的教学方式、建议的教学进度
   - 如：建议教师下节课关注XXX、复习XXX、调整教学进度XXX
   - 总结整体掌握情况：XX%学生掌握了XXX，XX%仍然对XXX知识感到困扰

如果提供了课件内容（pdf_context），请结合课件的教学思路和实际内容进行分析，使建议更有针对性。

请输出 JSON，包含以下字段：
- answer_analysis: string — 答题情况与错题总结（可包含换行，使用\n）
- feedback_summary: string — 学生反馈总结（可包含换行，使用\n）
- teaching_suggestions: string — 教学建议（可包含换行，使用\n）

注意：语言要专业、清晰，像教学顾问向教师汇报一样。数据引用要准确。只输出合法 JSON，不要 markdown 代码块或其他内容。`

func (c *Client) AdminSummarize(ctx context.Context, in AdminSummarizeInput) (AdminSummary, error) {
	c.mu.RLock()
	endpoint := c.endpoint
	apiKey := c.apiKey
	model := c.model
	c.mu.RUnlock()

	if strings.TrimSpace(endpoint) == "" {
		return AdminSummary{}, fmt.Errorf("AI endpoint 未配置")
	}

	userMsg, _ := json.Marshal(in)
	url := resolveEndpoint(endpoint)
	payload := map[string]any{
		"model": model,
		"messages": []map[string]string{
			{"role": "system", "content": adminSystemPrompt},
			{"role": "user", "content": string(userMsg)},
		},
		"temperature": 0.4,
	}
	body, _ := json.Marshal(payload)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		c.setLastError(err.Error())
		return AdminSummary{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}

	resp, err := c.httpCli.Do(req)
	if err != nil {
		c.setLastError(err.Error())
		return AdminSummary{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		msg := fmt.Sprintf("AI 返回状态码 %d", resp.StatusCode)
		c.setLastError(msg)
		return AdminSummary{}, fmt.Errorf("%s", msg)
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
		return AdminSummary{}, err
	}
	if len(chatResp.Choices) == 0 {
		msg := "AI 返回空 choices"
		c.setLastError(msg)
		return AdminSummary{}, fmt.Errorf("%s", msg)
	}

	content := stripCodeFence(strings.TrimSpace(chatResp.Choices[0].Message.Content))
	var summary AdminSummary
	if err := json.Unmarshal([]byte(content), &summary); err != nil {
		c.setLastError("解析 AI JSON 失败: " + err.Error())
		return AdminSummary{}, err
	}

	c.setLastSuccess(time.Now())
	return summary, nil
}
