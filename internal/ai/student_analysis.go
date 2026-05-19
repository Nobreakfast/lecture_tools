// Copyright 2024-2026 course-assistant contributors.
// SPDX-License-Identifier: MIT

package ai

import (
	"context"
	"fmt"
	"strings"
)

// StudentAnalysisInput carries the raw collected data and optional teacher note
// for the AI to produce a deep student analysis report.
type StudentAnalysisInput struct {
	RawData     string `json:"raw_data"`
	TeacherNote string `json:"teacher_note,omitempty"`
}

// StudentAnalysis is the structured output from the AI analysis.
type StudentAnalysis struct {
	ParticipationPattern        string `json:"participation_pattern"`
	KnowledgeProfile            string `json:"knowledge_profile"`
	EngagementSignals           string `json:"engagement_signals"`
	PsychologicalObservations   string `json:"psychological_observations"`
	CommunicationSuggestions    string `json:"communication_suggestions"`
}

const studentAnalysisSystemPrompt = `你是一位经验丰富的大学教学行为分析师。教师希望你根据一个学生在整个课程中的所有行为数据，生成一份深度分析报告。

你将获得以下原始数据：
- 学生在所有小测中的逐题作答详情（选择题的选项、简答题原文、调研题回答）
- 学生的作业提交记录、评分和反馈
- 学生在 Q&A 系统中的发言和互动记录

请输出 JSON，包含以下五个字段：

1. participation_pattern（参与模式分析）：
   - 分析学生的参与频率和完成度（参加了几次小测、完成度如何、是否有缺席）
   - 描述作答行为模式（是否有大量空白作答、是否存在随机选择的迹象、答题时间规律）
   - 评估参与度变化趋势（早期vs后期、是否逐渐退出）

2. knowledge_profile（知识掌握画像）：
   - 总结强项知识点和弱项知识点
   - 分析错题模式（是概念性错误、粗心、还是完全不理解）
   - 如果有多次小测数据，分析知识掌握的变化趋势

3. engagement_signals（参与度信号）：
   - 分析简答题和调研反馈的质量（是否认真填写、字数和内容丰富度）
   - 分析 Q&A 互动情况（主动提问频率、问题质量）
   - 分析作业完成情况和质量
   - 识别积极信号和消极信号

4. psychological_observations（行为观察）：
   - 基于上述行为数据，推测学生可能的学习状态（如：迷茫、挫败、缺乏动力、对课程内容无兴趣、过于焦虑等）
   - 识别需要关注的信号（如：突然退出、持续空白作答、反馈中的负面情绪）
   - 重要：这是基于行为数据的观察推测，不是心理诊断。请明确标注这一点

5. communication_suggestions（沟通建议）：
   - 基于以上分析，给出教师与该学生沟通的具体建议
   - 包括：合适的沟通时机、切入话题、谈话策略、应避免的方式
   - 给出 2-3 个具体可执行的行动建议

如果教师提供了额外说明（如"该学生上课不参与"、"不回应教师"等），请结合教师的观察一起分析。

语言要求：
- 专业、客观、有同理心
- 避免标签化或评判性语言
- 始终从"帮助学生"的角度出发
- 数据引用要具体，不要空泛
- 每个字段的内容可以包含换行（使用\n）

只输出合法 JSON，不要 markdown 代码块或其他内容。`

func (c *Client) StudentDeepAnalysis(ctx context.Context, in StudentAnalysisInput) (string, error) {
	var userMsg strings.Builder
	userMsg.WriteString(in.RawData)
	if strings.TrimSpace(in.TeacherNote) != "" {
		userMsg.WriteString("\n\n【教师额外观察】\n")
		userMsg.WriteString(in.TeacherNote)
	}

	content, err := c.chat(ctx, studentAnalysisSystemPrompt, userMsg.String(), 0.4)
	if err != nil {
		return fallbackStudentAnalysis(in), err
	}

	var analysis StudentAnalysis
	if err := unmarshalAIJSONObject(content, &analysis); err != nil {
		c.setLastError("解析 AI JSON 失败: " + err.Error())
		return fallbackStudentAnalysis(in), nil
	}
	return formatStudentAnalysis(analysis), nil
}

func formatStudentAnalysis(a StudentAnalysis) string {
	var b strings.Builder
	b.WriteString("## 参与模式分析\n")
	b.WriteString(a.ParticipationPattern)
	b.WriteString("\n\n## 知识掌握画像\n")
	b.WriteString(a.KnowledgeProfile)
	b.WriteString("\n\n## 参与度信号\n")
	b.WriteString(a.EngagementSignals)
	b.WriteString("\n\n## 行为观察\n")
	b.WriteString(a.PsychologicalObservations)
	b.WriteString("\n\n## 沟通建议\n")
	b.WriteString(a.CommunicationSuggestions)
	return b.String()
}

func fallbackStudentAnalysis(in StudentAnalysisInput) string {
	lines := strings.Split(in.RawData, "\n")
	quizCount := 0
	hwCount := 0
	qaCount := 0
	for _, line := range lines {
		if strings.HasPrefix(line, "### 小测 ") {
			quizCount++
		}
		if strings.HasPrefix(line, "### 作业 ") {
			hwCount++
		}
		if strings.HasPrefix(line, "### #") {
			qaCount++
		}
	}
	note := ""
	if strings.TrimSpace(in.TeacherNote) != "" {
		note = fmt.Sprintf("\n教师备注：%s", in.TeacherNote)
	}
	return fmt.Sprintf("（AI 返回格式异常，已生成本地兜底摘要。）\n"+
		"该学生共有 %d 条小测记录、%d 条作业记录、%d 条 Q&A 记录。"+
		"请结合原始数据进行人工分析。%s",
		quizCount, hwCount, qaCount, note)
}

// StudentAnalysisJSON returns the raw JSON analysis instead of formatted text.
func (c *Client) StudentAnalysisJSON(ctx context.Context, in StudentAnalysisInput) (StudentAnalysis, error) {
	var userMsg strings.Builder
	userMsg.WriteString(in.RawData)
	if strings.TrimSpace(in.TeacherNote) != "" {
		userMsg.WriteString("\n\n【教师额外观察】\n")
		userMsg.WriteString(in.TeacherNote)
	}

	content, err := c.chat(ctx, studentAnalysisSystemPrompt, userMsg.String(), 0.4)
	if err != nil {
		return StudentAnalysis{}, err
	}

	var analysis StudentAnalysis
	if err := unmarshalAIJSONObject(content, &analysis); err != nil {
		c.setLastError("解析 AI JSON 失败: " + err.Error())
		return StudentAnalysis{}, err
	}
	return analysis, nil
}

