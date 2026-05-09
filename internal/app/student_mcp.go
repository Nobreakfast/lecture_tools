// Copyright 2024-2026 course-assistant contributors.
// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"

	"course-assistant/internal/domain"
)

func (s *Server) studentMCPQuizHistory(ctx context.Context, submission *domain.HomeworkSubmission, limit int) (string, error) {
	if submission == nil {
		return "", fmt.Errorf("unauthorized")
	}
	course, err := s.store.GetCourse(ctx, submission.CourseID)
	if err != nil || course == nil {
		return "", fmt.Errorf("课程不存在")
	}
	attempts, err := s.store.ListAttemptsByCourse(ctx, submission.CourseID)
	if err != nil {
		return "", fmt.Errorf("读取小测记录失败: %w", err)
	}
	matched := make([]domain.Attempt, 0)
	for _, attempt := range attempts {
		if strings.TrimSpace(attempt.StudentNo) == strings.TrimSpace(submission.StudentNo) {
			matched = append(matched, attempt)
		}
	}
	if len(matched) == 0 {
		return "当前课程暂未找到你的历史小测记录。请确认作业入口中使用的学号与小测一致。", nil
	}
	sort.Slice(matched, func(i, j int) bool {
		return matched[i].UpdatedAt.After(matched[j].UpdatedAt)
	})
	if len(matched) > limit {
		matched = matched[:limit]
	}

	s.quizMu.RLock()
	loadedQuiz := s.courseQuizzes[submission.CourseID]
	s.quizMu.RUnlock()
	quizTitles := s.studentQuizTitleMap(course, loadedQuiz, attempts)

	var b strings.Builder
	b.WriteString(fmt.Sprintf("学生：%s（%s，%s）\n", submission.Name, submission.StudentNo, submission.ClassName))
	b.WriteString(fmt.Sprintf("课程：%s\n\n", course.DisplayName))
	b.WriteString("| 小测 | 状态 | 次数 | 得分 | 提交/更新时间 |\n")
	b.WriteString("|------|------|------|------|---------------|\n")
	weakTags := map[string]int{}
	for _, attempt := range matched {
		title := strings.TrimSpace(quizTitles[attempt.QuizID])
		if title == "" {
			title = attempt.QuizID
		}
		score := "未计算（题库未加载或无法匹配）"
		if q := s.loadCourseQuizFromBank(submission.CourseID, course.TeacherID, course.Slug, attempt.QuizID); q != nil && attempt.Status == domain.StatusSubmitted {
			correct, total := s.calcScore(ctx, q, attempt.ID)
			score = fmt.Sprintf("%d/%d", correct, total)
			answers, _ := s.store.GetAnswers(ctx, attempt.ID)
			for _, question := range shuffledQuestions(q, attempt.ID) {
				if question.Type == domain.QuestionSurvey || question.Type == domain.QuestionShortAnswer || strings.TrimSpace(question.KnowledgeTag) == "" {
					continue
				}
				if !isCorrectAnswer(question, answers[question.ID]) {
					weakTags[question.KnowledgeTag]++
				}
			}
		}
		when := attempt.UpdatedAt.Format("2006-01-02 15:04")
		if attempt.SubmittedAt != nil {
			when = attempt.SubmittedAt.Format("2006-01-02 15:04")
		}
		b.WriteString(fmt.Sprintf("| %s（%s） | %s | %d | %s | %s |\n",
			escapeMCPTableCell(title), escapeMCPTableCell(attempt.QuizID), attempt.Status, attempt.AttemptNo, score, when))
	}
	if len(weakTags) > 0 {
		type kv struct {
			k string
			v int
		}
		items := make([]kv, 0, len(weakTags))
		for k, v := range weakTags {
			items = append(items, kv{k: k, v: v})
		}
		sort.Slice(items, func(i, j int) bool {
			if items[i].v != items[j].v {
				return items[i].v > items[j].v
			}
			return items[i].k < items[j].k
		})
		if len(items) > 5 {
			items = items[:5]
		}
		b.WriteString("\n学习建议线索：优先复盘这些高频薄弱知识点：")
		for i, item := range items {
			if i > 0 {
				b.WriteString("、")
			}
			b.WriteString(fmt.Sprintf("%s（%d 次）", item.k, item.v))
		}
		b.WriteString("。请结合课堂资料和错题原因制定复习计划。")
	} else {
		b.WriteString("\n学习建议线索：当前没有可计算的错题知识点。可以先复盘最近一次小测的题目、解释和课堂资料。")
	}
	return b.String(), nil
}

func (s *Server) studentMCPHomeworkStatus(ctx context.Context, submission *domain.HomeworkSubmission) (string, error) {
	if submission == nil {
		return "", fmt.Errorf("unauthorized")
	}
	course, err := s.store.GetCourse(ctx, submission.CourseID)
	if err != nil || course == nil {
		return "", fmt.Errorf("课程不存在")
	}
	fresh, err := s.store.GetHomeworkSubmissionByID(ctx, submission.ID)
	if err != nil {
		return "", fmt.Errorf("读取作业状态失败: %w", err)
	}
	var b strings.Builder
	b.WriteString(fmt.Sprintf("课程：%s\n", course.DisplayName))
	b.WriteString(fmt.Sprintf("作业编号：%s\n", fresh.AssignmentID))
	b.WriteString(fmt.Sprintf("学生：%s（%s，%s）\n", fresh.Name, fresh.StudentNo, fresh.ClassName))
	b.WriteString(fmt.Sprintf("报告：%s\n", yesNoFile(fresh.ReportOriginalName)))
	b.WriteString(fmt.Sprintf("代码：%s\n", yesNoFile(fresh.CodeOriginalName)))
	b.WriteString(fmt.Sprintf("补充：%s\n", yesNoFile(fresh.ExtraOriginalName)))
	if fresh.Score != nil {
		b.WriteString(fmt.Sprintf("教师评分：%.1f\n", *fresh.Score))
	}
	if strings.TrimSpace(fresh.Feedback) != "" {
		b.WriteString("教师反馈：" + strings.TrimSpace(fresh.Feedback) + "\n")
	}
	if strings.TrimSpace(fresh.AIPregradeFeedback) != "" {
		b.WriteString("AI 预批改反馈：" + strings.TrimSpace(fresh.AIPregradeFeedback) + "\n")
	}
	b.WriteString("如问题需要教师确认，请调用 create_student_qa_issue 新建 Q&A。")
	return b.String(), nil
}

func (s *Server) studentMCPCreateQAIssue(ctx context.Context, submission *domain.HomeworkSubmission, title, summary string) (string, error) {
	if submission == nil {
		return "", fmt.Errorf("unauthorized")
	}
	summary = strings.TrimSpace(summary)
	if summary == "" {
		return "", fmt.Errorf("问题摘要不能为空")
	}
	if len([]rune(summary)) > 3000 {
		summary = string([]rune(summary)[:3000])
	}
	course, err := s.store.GetCourse(ctx, submission.CourseID)
	if err != nil || course == nil {
		return "", fmt.Errorf("课程不存在")
	}
	if title = strings.TrimSpace(title); title == "" {
		title = summary
	}
	if len([]rune(title)) > 80 {
		title = string([]rune(title)[:80]) + "..."
	}
	now := time.Now()
	issue := &domain.QAIssue{
		CourseID:     submission.CourseID,
		Course:       course.Slug,
		AssignmentID: submission.AssignmentID,
		StudentNo:    submission.StudentNo,
		Title:        title,
		Status:       "open",
		MessageCount: 1,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	issueID, err := s.store.CreateQAIssue(ctx, issue)
	if err != nil {
		return "", fmt.Errorf("创建 Q&A 失败: %w", err)
	}
	msg := &domain.QAMessage{
		IssueID:   int(issueID),
		Sender:    "student",
		Content:   summary,
		CreatedAt: now,
	}
	if _, err := s.store.CreateQAMessage(ctx, msg); err != nil {
		return "", fmt.Errorf("保存 Q&A 消息失败: %w", err)
	}
	link := s.studentQAIssueLink(submission.CourseID, submission.AssignmentID, int(issueID))
	return fmt.Sprintf("已新建 Q&A #%d。问题已反馈给教师，请过几天再到系统 Q&A 中查看教师回复。链接：%s", issueID, link), nil
}

func (s *Server) studentQAIssueLink(courseID int, assignmentID string, issueID int) string {
	path := fmt.Sprintf("/student/qa?course_id=%d&assignment_id=%s&focus=%d", courseID, url.QueryEscape(assignmentID), issueID)
	if strings.TrimSpace(s.cfg.BaseURL) == "" {
		return s.pathPrefix() + path
	}
	return strings.TrimRight(s.cfg.BaseURL, "/") + path
}

func escapeMCPTableCell(value string) string {
	value = strings.ReplaceAll(value, "\r\n", " ")
	value = strings.ReplaceAll(value, "\n", " ")
	value = strings.ReplaceAll(value, "|", "\\|")
	return strings.TrimSpace(value)
}

func yesNoFile(name string) string {
	if strings.TrimSpace(name) == "" {
		return "未上传"
	}
	return "已上传（" + strings.TrimSpace(name) + "）"
}
