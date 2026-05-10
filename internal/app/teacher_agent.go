// Copyright 2024-2026 course-assistant contributors.
// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"

	"course-assistant/internal/domain"
)

const teacherAgentMaxMessages = 8

func (s *Server) apiTeacherAgentChat(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	sess := s.requireTeacherOrAdmin(r)
	if sess == nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	var req struct {
		CourseID string `json:"course_id"`
		Message  string `json:"message"`
		Messages []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid body", http.StatusBadRequest)
		return
	}
	req.Message = strings.TrimSpace(req.Message)
	if req.Message == "" {
		http.Error(w, "问题不能为空", http.StatusBadRequest)
		return
	}
	req.Message = truncateAgentText(req.Message)

	ctxText, err := s.teacherAgentContext(r, sess, strings.TrimSpace(req.CourseID))
	if err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}
	prompt := s.teacherAgentPrompt(ctxText, req.Message, req.Messages)
	answer, err := s.aiClient.TeacherAgentChat(r.Context(), prompt)
	if err != nil {
		writeJSONStatus(w, http.StatusBadGateway, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, map[string]any{"answer": answer})
}

func (s *Server) teacherAgentPrompt(ctxText, latest string, messages []struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}) string {
	var b strings.Builder
	b.WriteString("以下是当前教师可访问的只读课堂数据快照。请只基于这些数据回答。\n\n")
	b.WriteString(ctxText)
	b.WriteString("\n\n最近对话：\n")
	start := 0
	if len(messages) > teacherAgentMaxMessages {
		start = len(messages) - teacherAgentMaxMessages
	}
	for _, msg := range messages[start:] {
		role := strings.TrimSpace(msg.Role)
		content := strings.TrimSpace(msg.Content)
		if content == "" {
			continue
		}
		content = truncateAgentText(content)
		if role != "assistant" {
			role = "teacher"
		}
		b.WriteString(fmt.Sprintf("%s: %s\n", role, content))
	}
	b.WriteString("teacher: ")
	b.WriteString(latest)
	return b.String()
}

func (s *Server) teacherAgentContext(r *http.Request, sess *authSession, courseID string) (string, error) {
	if courseID != "" {
		q := r.URL.Query()
		q.Set("course_id", courseID)
		r2 := r.Clone(r.Context())
		r2.Method = http.MethodGet
		r2.URL.RawQuery = q.Encode()
		_, course, err := s.resolveTeacherCourse(r2, sess)
		if err != nil {
			return "", err
		}
		return s.teacherAgentCourseContext(r.Context(), course), nil
	}

	courses, err := s.store.ListCoursesByTeacher(r.Context(), sess.TeacherID)
	if err != nil {
		return "", fmt.Errorf("读取课程列表失败")
	}
	sort.Slice(courses, func(i, j int) bool { return courses[i].ID < courses[j].ID })
	if len(courses) == 0 {
		return "该教师暂无课程。", nil
	}
	if len(courses) > 5 {
		courses = courses[:5]
	}
	var b strings.Builder
	b.WriteString("教师课程概览（如需更详细分析，建议教师先选择具体课程）：\n")
	for _, course := range courses {
		b.WriteString("\n")
		b.WriteString(s.teacherAgentCourseContext(r.Context(), &course))
	}
	return b.String(), nil
}

func (s *Server) teacherAgentCourseContext(ctx context.Context, course *domain.Course) string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("课程：%s（ID: %d，标识: %s）\n", teacherAgentCourseName(course), course.ID, course.Slug))

	s.quizMu.RLock()
	q := s.courseQuizzes[course.ID]
	s.quizMu.RUnlock()
	if q != nil {
		b.WriteString(fmt.Sprintf("当前加载小测：%s - %s，共 %d 题。\n", q.QuizID, q.Title, len(q.Questions)))
	} else {
		b.WriteString("当前未加载小测。\n")
	}

	attempts, err := s.store.ListAttemptsByCourse(ctx, course.ID)
	if err != nil {
		b.WriteString("小测记录：读取失败。\n")
	} else {
		bestItems := s.teacherCourseBestAttempts(ctx, course, q, attempts)
		b.WriteString(fmt.Sprintf("小测记录：共 %d 名/组学生记录。\n", len(bestItems)))
		limit := len(bestItems)
		if limit > 40 {
			limit = 40
		}
		for i := 0; i < limit; i++ {
			item := bestItems[i]
			a := item.Attempt
			score := "未计分"
			if item.QuizLoaded {
				score = fmt.Sprintf("%d/%d", item.Correct, item.Total)
			}
			b.WriteString(fmt.Sprintf("- 学生：%s，学号：%s，班级：%s，小测：%s，状态：%s，次数：%d，得分：%s，更新时间：%s\n",
				a.Name, a.StudentNo, a.ClassName, a.QuizID, a.Status, a.AttemptNo, score, a.UpdatedAt.Format("2006-01-02 15:04")))
		}
		if len(bestItems) > limit {
			b.WriteString(fmt.Sprintf("- 其余 %d 条小测记录已省略。\n", len(bestItems)-limit))
		}
	}

	homework, err := s.store.ListHomeworkSubmissions(ctx, course.ID, course.Slug, "")
	if err != nil {
		b.WriteString("作业提交：读取失败。\n")
	} else {
		b.WriteString(fmt.Sprintf("作业提交：共 %d 条。\n", len(homework)))
		limit := len(homework)
		if limit > 30 {
			limit = 30
		}
		for i := 0; i < limit; i++ {
			h := homework[i]
			score := "未评分"
			if h.Score != nil {
				score = fmt.Sprintf("%.1f", *h.Score)
			}
			b.WriteString(fmt.Sprintf("- 作业：%s，学生：%s，学号：%s，班级：%s，文件：%s，评分：%s，更新时间：%s\n",
				h.AssignmentID, h.Name, h.StudentNo, h.ClassName, teacherAgentHomeworkFiles(h), score, h.UpdatedAt.Format("2006-01-02 15:04")))
		}
		if len(homework) > limit {
			b.WriteString(fmt.Sprintf("- 其余 %d 条作业记录已省略。\n", len(homework)-limit))
		}
	}

	materials, err := s.scanMaterialsFromDir(s.metadataMaterialsDir(course.TeacherID, course.Slug), course.Slug, true)
	if err == nil && len(materials) > 0 {
		b.WriteString(fmt.Sprintf("课程资料：共 %d 个文件。\n", len(materials)))
		limit := len(materials)
		if limit > 20 {
			limit = 20
		}
		for i := 0; i < limit; i++ {
			m := materials[i]
			files := make([]string, 0, len(m.Downloads))
			for _, d := range m.Downloads {
				files = append(files, d.File)
			}
			b.WriteString(fmt.Sprintf("- %s：%s\n", m.Stem, strings.Join(files, "、")))
		}
	}
	return b.String()
}

func teacherAgentHomeworkFiles(h domain.HomeworkSubmission) string {
	files := make([]string, 0, 3)
	if h.ReportOriginalName != "" {
		files = append(files, "报告")
	}
	if h.CodeOriginalName != "" {
		files = append(files, "代码")
	}
	if h.ExtraOriginalName != "" {
		files = append(files, "附件")
	}
	if len(files) == 0 {
		return "未上传"
	}
	return strings.Join(files, "/")
}

func teacherAgentCourseName(course *domain.Course) string {
	if strings.TrimSpace(course.DisplayName) != "" {
		return strings.TrimSpace(course.DisplayName)
	}
	if strings.TrimSpace(course.Name) != "" {
		return strings.TrimSpace(course.Name)
	}
	return strings.TrimSpace(course.Slug)
}
