// Copyright 2024-2026 course-assistant contributors.
// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"course-assistant/internal/domain"
	"course-assistant/internal/pdftext"
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
			score = fmt.Sprintf("%s/%d", formatScoreValue(correct), total)
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
	b.WriteString("如问题需要教师确认，请将学生诉求整理为 Q&A。")
	return b.String(), nil
}

func (s *Server) studentMCPSearchVisibleQAIssues(ctx context.Context, submission *domain.HomeworkSubmission, query string, limit int) (string, error) {
	if submission == nil {
		return "", fmt.Errorf("unauthorized")
	}
	if limit <= 0 || limit > 5 {
		limit = 3
	}
	issues, err := s.store.ListQAIssues(ctx, submission.CourseID, submission.AssignmentID, false)
	if err != nil {
		return "", fmt.Errorf("读取 Q&A 失败: %w", err)
	}
	type hit struct {
		issue    domain.QAIssue
		messages []domain.QAMessage
		score    int
	}
	hits := make([]hit, 0)
	for _, issue := range issues {
		messages, _ := s.store.ListQAMessages(ctx, issue.ID)
		text := issue.Title
		for _, msg := range messages {
			text += "\n" + msg.Content
		}
		score := studentAgentTextScore(query, text)
		if score == 0 && strings.TrimSpace(query) != "" {
			continue
		}
		hits = append(hits, hit{issue: issue, messages: messages, score: score})
	}
	sort.Slice(hits, func(i, j int) bool {
		if hits[i].score != hits[j].score {
			return hits[i].score > hits[j].score
		}
		if hits[i].issue.Status != hits[j].issue.Status {
			return hits[i].issue.Status == "resolved"
		}
		return hits[i].issue.UpdatedAt.After(hits[j].issue.UpdatedAt)
	})
	if len(hits) > limit {
		hits = hits[:limit]
	}
	if len(hits) == 0 {
		return "未找到与当前问题明显相关的已有 Q&A。", nil
	}
	var b strings.Builder
	b.WriteString("以下是当前课程/作业范围内与学生问题相关的已有 Q&A。若已有回复足以回答，请复用其结论；若已有未解决相似问题，请不要重复创建。\n")
	for _, h := range hits {
		b.WriteString(fmt.Sprintf("\n#%d [%s] %s\n", h.issue.ID, h.issue.Status, strings.TrimSpace(h.issue.Title)))
		msgLimit := len(h.messages)
		if msgLimit > 4 {
			msgLimit = 4
		}
		for _, msg := range h.messages[:msgLimit] {
			content := truncateAgentText(msg.Content)
			if len([]rune(content)) > 500 {
				content = string([]rune(content)[:500]) + "..."
			}
			b.WriteString(fmt.Sprintf("- %s: %s\n", msg.Sender, strings.TrimSpace(content)))
		}
	}
	return b.String(), nil
}

func (s *Server) studentMCPVisibleCourseMaterials(ctx context.Context, submission *domain.HomeworkSubmission) (string, error) {
	course, err := s.studentAgentCourse(ctx, submission)
	if err != nil {
		return "", err
	}
	materials, err := s.scanMaterialsFromDir(s.metadataMaterialsDir(course.TeacherID, course.Slug), course.Slug, false)
	if err != nil {
		return "", fmt.Errorf("读取课程资料失败: %w", err)
	}
	if len(materials) == 0 {
		return "该课程暂无学生可见的课程资料。", nil
	}
	var b strings.Builder
	b.WriteString("学生可见课程资料：\n")
	for _, group := range materials {
		files := make([]string, 0, len(group.Downloads))
		for _, item := range group.Downloads {
			files = append(files, item.File)
		}
		b.WriteString(fmt.Sprintf("- %s：%s\n", group.Stem, strings.Join(files, "、")))
	}
	return b.String(), nil
}

func (s *Server) studentMCPReadVisibleMaterialText(ctx context.Context, submission *domain.HomeworkSubmission, file string) (string, error) {
	course, err := s.studentAgentCourse(ctx, submission)
	if err != nil {
		return "", err
	}
	name, ext, err := normalizeMaterialFilename(file, "")
	if err != nil {
		return "", fmt.Errorf("资料文件名无效")
	}
	if !s.studentCanReadMaterial(ctx, course, name) {
		return "", fmt.Errorf("资料不存在或当前不可见")
	}
	path := filepath.Join(s.metadataMaterialsDir(course.TeacherID, course.Slug), name)
	text, err := extractStudentAgentReadableText(path, ext)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("资料 %s 的可读取内容：\n%s", name, text), nil
}

func (s *Server) studentMCPVisibleAssignmentContext(ctx context.Context, submission *domain.HomeworkSubmission) (string, error) {
	course, err := s.studentAgentCourse(ctx, submission)
	if err != nil {
		return "", err
	}
	visibility := s.loadHomeworkAssignmentVisibility(ctx)
	if homeworkAssignmentHidden(visibility, course.Slug, submission.AssignmentID) {
		return "当前作业资料对学生不可见。", nil
	}
	files := s.listHomeworkAssignmentFiles(course.Slug, course.ID, submission.AssignmentID)
	if len(files) == 0 {
		return "当前作业没有可见附件。", nil
	}
	var b strings.Builder
	b.WriteString(fmt.Sprintf("当前作业 %s 的可见附件：\n", submission.AssignmentID))
	for _, item := range files {
		b.WriteString(fmt.Sprintf("- %s\n", item["name"]))
	}
	for _, item := range files {
		name, _ := item["name"].(string)
		text, ok := s.readVisibleAssignmentFilePreview(ctx, course, submission.AssignmentID, name)
		if ok {
			b.WriteString(fmt.Sprintf("\n附件 %s 可读取内容：\n%s\n", name, text))
			break
		}
	}
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
	summary = redactQAIssueStudentIdentity(summary, submission)
	course, err := s.store.GetCourse(ctx, submission.CourseID)
	if err != nil || course == nil {
		return "", fmt.Errorf("课程不存在")
	}
	if title = strings.TrimSpace(title); title == "" {
		title = summary
	}
	title = redactQAIssueStudentIdentity(title, submission)
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
		Hidden:       true,
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
	returnTo := "/?tab=tab-homework"
	if strings.TrimSpace(assignmentID) != "" {
		returnTo += "&assignment_id=" + url.QueryEscape(assignmentID)
	}
	path := fmt.Sprintf("/student/qa?course_id=%d&assignment_id=%s&focus=%d&return_to=%s",
		courseID, url.QueryEscape(assignmentID), issueID, url.QueryEscape(returnTo))
	if strings.TrimSpace(s.cfg.BaseURL) == "" {
		return s.pathPrefix() + path
	}
	return strings.TrimRight(s.cfg.BaseURL, "/") + path
}

func (s *Server) studentAgentCourse(ctx context.Context, submission *domain.HomeworkSubmission) (*domain.Course, error) {
	if submission == nil {
		return nil, fmt.Errorf("unauthorized")
	}
	course, err := s.store.GetCourse(ctx, submission.CourseID)
	if err != nil || course == nil {
		return nil, fmt.Errorf("课程不存在")
	}
	return course, nil
}

func (s *Server) studentCanReadMaterial(ctx context.Context, course *domain.Course, name string) bool {
	materials, err := s.scanMaterialsFromDir(s.metadataMaterialsDir(course.TeacherID, course.Slug), course.Slug, false)
	if err != nil {
		return false
	}
	for _, group := range materials {
		for _, item := range group.Downloads {
			if item.File == name && item.Visible {
				return true
			}
		}
	}
	return false
}

func extractStudentAgentReadableText(path, ext string) (string, error) {
	switch strings.ToLower(ext) {
	case ".pdf":
		text, err := pdftext.ExtractText(path)
		if err != nil {
			return "", err
		}
		return truncateAgentText(text), nil
	case ".txt", ".md", ".csv", ".json", ".yaml", ".yml", ".py", ".ipynb":
		data, err := os.ReadFile(path)
		if err != nil {
			return "", err
		}
		return truncateAgentText(string(data)), nil
	default:
		return "该文件类型暂不支持正文提取，只能作为文件名和附件信息参考。", nil
	}
}

func (s *Server) readVisibleAssignmentFilePreview(ctx context.Context, course *domain.Course, assignmentID, name string) (string, bool) {
	if name == "" {
		return "", false
	}
	visibility := s.loadHomeworkAssignmentVisibility(ctx)
	if homeworkAssignmentHidden(visibility, course.Slug, assignmentID) {
		return "", false
	}
	ext := strings.ToLower(filepath.Ext(name))
	path := filepath.Join(s.metadataHomeworkAssignmentDir(course.TeacherID, course.Slug, assignmentID), name)
	if _, err := os.Stat(path); err != nil {
		path = filepath.Join(s.homeworkAssignmentDir(course.Slug, assignmentID), name)
	}
	if _, err := os.Stat(path); err != nil && name == assignmentID+".pdf" {
		path = s.homeworkLegacyAssignmentPath(course.Slug, assignmentID)
	}
	if _, err := os.Stat(path); err != nil {
		return "", false
	}
	text, err := extractStudentAgentReadableText(path, ext)
	if err != nil {
		return "", false
	}
	return text, true
}

func studentAgentTextScore(query, text string) int {
	query = strings.ToLower(strings.TrimSpace(query))
	text = strings.ToLower(strings.TrimSpace(text))
	if query == "" || text == "" {
		return 0
	}
	if strings.Contains(text, query) {
		return 100 + len([]rune(query))
	}
	score := 0
	seen := map[string]struct{}{}
	for _, token := range strings.FieldsFunc(query, func(r rune) bool {
		return r == ' ' || r == '\n' || r == '\t' || r == '，' || r == '。' || r == '？' || r == '?' || r == '、' || r == ',' || r == '.'
	}) {
		token = strings.TrimSpace(token)
		if len([]rune(token)) < 2 {
			continue
		}
		if _, ok := seen[token]; ok {
			continue
		}
		seen[token] = struct{}{}
		if strings.Contains(text, token) {
			score += len([]rune(token))
		}
	}
	return score
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
