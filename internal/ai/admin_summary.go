// Copyright 2024-2026 course-assistant contributors.
// SPDX-License-Identifier: MIT

package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
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
	AvgTotal      float64             `json:"avg_total"`
	QuestionStats []AdminQuestionStat `json:"question_stats"`
	FeedbackItems []AdminFeedbackItem `json:"feedback_items"`
	PDFContext    string              `json:"pdf_context,omitempty"`
}

type AdminSummary struct {
	AnswerAnalysis      string `json:"answer_analysis"`
	FeedbackSummary     string `json:"feedback_summary"`
	TeachingSuggestions string `json:"teaching_suggestions"`
}

type HistoryQuizStat struct {
	QuizID       string  `json:"quiz_id"`
	QuizTitle    string  `json:"quiz_title"`
	StudentCount int     `json:"student_count"`
	AvgScore     float64 `json:"avg_score"`
	AvgTotal     float64 `json:"avg_total"`
}

type HistorySummarizeInput struct {
	CourseName string            `json:"course_name"`
	QuizStats  []HistoryQuizStat `json:"quiz_stats"`
}

type HistorySummary struct {
	OverallTrend        string `json:"overall_trend"`
	PerformanceInsights string `json:"performance_insights"`
	TeachingSuggestions string `json:"teaching_suggestions"`
}

const historySystemPrompt = `你是一位大学课堂教学分析助手。教师希望了解本学期多次小测的整体学生表现趋势。

请根据提供的多次小测统计数据，生成一份横跨多次小测的纵向分析报告。

要求分为以下三个部分：

1. **总体趋势分析**（overall_trend）：
   - 描述学生在多次小测中的成绩变化趋势（提升、下降还是平稳）
   - 引用具体的均分数据（请使用 avg_score/avg_total 字段）
   - 例：第一次小测均分 3/5，第三次均分 4.5/5，整体呈上升趋势

2. **表现洞察**（performance_insights）：
   - 对比各次小测的参与人数、均分情况，找出异常数据
   - 分析哪些知识点可能存在持续问题（如某次小测后均分明显下降）

3. **教学建议**（teaching_suggestions）：
   - 基于整体趋势，给出对后续教学的建议
   - 如需要复习的时间节点、需要关注的薄弱环节

请输出 JSON，包含以下字段：
- overall_trend: string — 总体趋势分析
- performance_insights: string — 表现洞察
- teaching_suggestions: string — 教学建议

只输出合法 JSON，不要 markdown 代码块或其他内容。`

const adminSystemPrompt = `你是一位大学课堂教学分析助手。教师刚完成一次课堂小测，请根据全班学生的答题统计数据和反馈，为教师生成一份教学总结报告。

要求分为以下三个部分：

1. **答题情况与错题总结**（answer_analysis）：
   - 概述全班整体答题表现（平均分、答题人数）。平均分请引用 avg_score/avg_total，其中 avg_total 为"计分题总数的平均值"，不包含 survey/short_answer
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

func (c *Client) HistorySummarize(ctx context.Context, in HistorySummarizeInput) (HistorySummary, error) {
	userMsg, _ := json.Marshal(in)
	content, err := c.chat(ctx, historySystemPrompt, string(userMsg), 0.4)
	if err != nil {
		return HistorySummary{}, err
	}

	var summary HistorySummary
	if err := unmarshalAIJSONObject(content, &summary); err != nil {
		c.setLastError("解析 AI JSON 失败: " + err.Error())
		return fallbackHistorySummary(in), nil
	}
	return summary, nil
}

func (c *Client) AdminSummarize(ctx context.Context, in AdminSummarizeInput) (AdminSummary, error) {
	userMsg, _ := json.Marshal(in)
	content, err := c.chat(ctx, adminSystemPrompt, string(userMsg), 0.4)
	if err != nil {
		return AdminSummary{}, err
	}

	var summary AdminSummary
	if err := unmarshalAIJSONObject(content, &summary); err != nil {
		c.setLastError("解析 AI JSON 失败: " + err.Error())
		return fallbackAdminSummary(in), nil
	}
	return summary, nil
}

func fallbackAdminSummary(in AdminSummarizeInput) AdminSummary {
	lowQuestions := append([]AdminQuestionStat(nil), in.QuestionStats...)
	sort.Slice(lowQuestions, func(i, j int) bool {
		if lowQuestions[i].CorrectRate == lowQuestions[j].CorrectRate {
			return lowQuestions[i].QuestionID < lowQuestions[j].QuestionID
		}
		return lowQuestions[i].CorrectRate < lowQuestions[j].CorrectRate
	})
	if len(lowQuestions) > 3 {
		lowQuestions = lowQuestions[:3]
	}

	var answer strings.Builder
	answer.WriteString("（AI 返回格式异常，已根据统计数据生成本地兜底总结。）\n")
	answer.WriteString(fmt.Sprintf("本次小测《%s》共有 %d 人提交，平均分 %.1f/%.1f。", in.QuizTitle, in.StudentCount, in.AvgScore, in.AvgTotal))
	if len(lowQuestions) > 0 {
		answer.WriteString("\n需要优先关注的题目：")
		for _, q := range lowQuestions {
			rate := q.CorrectRate * 100
			tag := strings.TrimSpace(q.KnowledgeTag)
			if tag == "" {
				tag = strings.TrimSpace(q.Stem)
			}
			answer.WriteString(fmt.Sprintf("\n- %s：正确率 %.1f%%", q.QuestionID, rate))
			if tag != "" {
				answer.WriteString("，涉及" + tag)
			}
			if len(q.CommonWrongAnswers) > 0 {
				answer.WriteString("，常见错误：" + strings.Join(q.CommonWrongAnswers, "、"))
			}
		}
	}

	var feedback strings.Builder
	if len(in.FeedbackItems) == 0 {
		feedback.WriteString("暂无问卷或简答反馈数据。")
	} else {
		feedback.WriteString("学生反馈概览：")
		for _, item := range in.FeedbackItems {
			feedback.WriteString("\n- " + item.QuestionID + "：" + strings.TrimSpace(item.Stem))
			if len(item.OptionCounts) > 0 {
				type optionCount struct {
					Label string
					Count int
				}
				options := make([]optionCount, 0, len(item.OptionCounts))
				for label, count := range item.OptionCounts {
					options = append(options, optionCount{Label: label, Count: count})
				}
				sort.Slice(options, func(i, j int) bool {
					if options[i].Count == options[j].Count {
						return options[i].Label < options[j].Label
					}
					return options[i].Count > options[j].Count
				})
				parts := make([]string, 0, len(options))
				for _, opt := range options {
					parts = append(parts, fmt.Sprintf("%s %d人", opt.Label, opt.Count))
				}
				feedback.WriteString("（" + strings.Join(parts, "；") + "）")
			}
			if len(item.TextSamples) > 0 {
				samples := append([]string(nil), item.TextSamples...)
				if len(samples) > 5 {
					samples = samples[:5]
				}
				feedback.WriteString("；代表性反馈：" + strings.Join(samples, "；"))
			}
		}
	}

	var suggestions strings.Builder
	suggestions.WriteString("建议下节课先用 5-10 分钟回顾低正确率题目对应的概念，并结合常见错误做针对性讲解。")
	if len(lowQuestions) > 0 {
		tags := make([]string, 0, len(lowQuestions))
		seen := map[string]bool{}
		for _, q := range lowQuestions {
			tag := strings.TrimSpace(q.KnowledgeTag)
			if tag == "" || seen[tag] {
				continue
			}
			seen[tag] = true
			tags = append(tags, tag)
		}
		if len(tags) > 0 {
			suggestions.WriteString("重点复习：" + strings.Join(tags, "、") + "。")
		}
	}
	if len(in.FeedbackItems) > 0 {
		suggestions.WriteString("同时参考学生反馈调整案例、练习或讲解节奏。")
	}

	return AdminSummary{
		AnswerAnalysis:      answer.String(),
		FeedbackSummary:     feedback.String(),
		TeachingSuggestions: suggestions.String(),
	}
}

func fallbackHistorySummary(in HistorySummarizeInput) HistorySummary {
	stats := append([]HistoryQuizStat(nil), in.QuizStats...)
	sort.Slice(stats, func(i, j int) bool { return stats[i].QuizID < stats[j].QuizID })
	if len(stats) == 0 {
		return HistorySummary{
			OverallTrend:        "（AI 返回格式异常，已根据统计数据生成本地兜底总结。）暂无历史小测数据。",
			PerformanceInsights: "暂无可分析的数据。",
			TeachingSuggestions: "建议在积累至少两次小测数据后再生成趋势总结。",
		}
	}

	first := stats[0]
	last := stats[len(stats)-1]
	trend := "基本平稳"
	firstRatio := scoreRatio(first)
	lastRatio := scoreRatio(last)
	if lastRatio > firstRatio+0.05 {
		trend = "整体提升"
	} else if lastRatio < firstRatio-0.05 {
		trend = "有所下降"
	}

	lowest := stats[0]
	highest := stats[0]
	for _, item := range stats[1:] {
		if scoreRatio(item) < scoreRatio(lowest) {
			lowest = item
		}
		if scoreRatio(item) > scoreRatio(highest) {
			highest = item
		}
	}

	return HistorySummary{
		OverallTrend: fmt.Sprintf(
			"（AI 返回格式异常，已根据统计数据生成本地兜底总结。）课程《%s》共记录 %d 次小测，首次数值为 %.1f/%.1f，最近一次为 %.1f/%.1f，趋势判断为：%s。",
			in.CourseName, len(stats), first.AvgScore, first.AvgTotal, last.AvgScore, last.AvgTotal, trend,
		),
		PerformanceInsights: fmt.Sprintf(
			"表现最好的是《%s》（%.1f/%.1f，%d 人），需要重点关注的是《%s》（%.1f/%.1f，%d 人）。",
			highest.QuizTitle, highest.AvgScore, highest.AvgTotal, highest.StudentCount, lowest.QuizTitle, lowest.AvgScore, lowest.AvgTotal, lowest.StudentCount,
		),
		TeachingSuggestions: "建议结合最低分小测对应的知识点安排一次短复盘，并在后续小测中保留同类题目观察是否改善。",
	}
}

func scoreRatio(stat HistoryQuizStat) float64 {
	if stat.AvgTotal <= 0 {
		return 0
	}
	return stat.AvgScore / stat.AvgTotal
}
