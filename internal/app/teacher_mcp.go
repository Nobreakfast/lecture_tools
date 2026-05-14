package app

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"course-assistant/internal/domain"
)

func (s *Server) teacherMCPReadCourse(ctx context.Context, sess *authSession, courseID int) (*domain.Course, error) {
	if sess == nil {
		return nil, fmt.Errorf("unauthorized")
	}
	course, err := s.store.GetCourse(ctx, courseID)
	if err != nil || course == nil {
		return nil, fmt.Errorf("课程不存在")
	}
	if sess.Role == domain.RoleAdmin || course.TeacherID == sess.TeacherID {
		return course, nil
	}
	if _, err := s.store.GetCourseTeacher(ctx, courseID, sess.TeacherID); err == nil {
		return course, nil
	}
	return nil, fmt.Errorf("无权限访问此课程")
}

func (s *Server) teacherMCPManageCourse(ctx context.Context, sess *authSession, courseID int) (*domain.Course, error) {
	course, err := s.teacherMCPReadCourse(ctx, sess, courseID)
	if err != nil {
		return nil, err
	}
	if sess.Role == domain.RoleAdmin || course.TeacherID == sess.TeacherID {
		return course, nil
	}
	member, err := s.store.GetCourseTeacher(ctx, courseID, sess.TeacherID)
	if err == nil && member.Permission == domain.CoursePermissionManage {
		return course, nil
	}
	return nil, fmt.Errorf("无权限修改此课程")
}

func (s *Server) teacherMCPQAIssues(ctx context.Context, sess *authSession, courseID int, assignmentID, status string, includeHidden bool, limit, maxMessages int) (string, error) {
	course, err := s.teacherMCPReadCourse(ctx, sess, courseID)
	if err != nil {
		return "", err
	}
	assignmentID = strings.TrimSpace(assignmentID)
	status = strings.ToLower(strings.TrimSpace(status))
	if status == "" {
		status = "open"
	}
	if status != "all" && status != "open" && status != "resolved" {
		return "", fmt.Errorf("status 只能是 open、resolved 或 all")
	}
	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}
	if maxMessages < 0 {
		maxMessages = 0
	}
	if maxMessages > 20 {
		maxMessages = 20
	}

	var issues []domain.QAIssue
	if assignmentID != "" {
		issues, err = s.store.ListQAIssues(ctx, courseID, assignmentID, includeHidden)
	} else {
		issues, err = s.store.ListQAIssuesByCourse(ctx, courseID, includeHidden)
	}
	if err != nil {
		return "", fmt.Errorf("读取 Q&A 失败: %w", err)
	}
	filtered := make([]domain.QAIssue, 0, len(issues))
	for _, issue := range issues {
		if status != "all" && strings.ToLower(strings.TrimSpace(issue.Status)) != status {
			continue
		}
		filtered = append(filtered, issue)
	}
	sort.SliceStable(filtered, func(i, j int) bool {
		if filtered[i].Pinned != filtered[j].Pinned {
			return filtered[i].Pinned
		}
		if filtered[i].Status != filtered[j].Status {
			return filtered[i].Status == "open"
		}
		if filtered[i].MessageCount != filtered[j].MessageCount {
			return filtered[i].MessageCount > filtered[j].MessageCount
		}
		return filtered[i].UpdatedAt.After(filtered[j].UpdatedAt)
	})
	if len(filtered) == 0 {
		return fmt.Sprintf("课程：%s\n当前筛选条件下暂无 Q&A。", teacherAgentCourseName(course)), nil
	}

	shown := filtered
	if len(shown) > limit {
		shown = shown[:limit]
	}
	var b strings.Builder
	b.WriteString(fmt.Sprintf("课程：%s（ID: %d）\n", teacherAgentCourseName(course), course.ID))
	if status == "open" {
		first := shown[0]
		b.WriteString(fmt.Sprintf("建议优先处理：#%d %s（%s，%d 条消息，更新时间 %s）。\n\n",
			first.ID, strings.TrimSpace(first.Title), teacherMCPQAIssueFlags(first), first.MessageCount, formatMCPTime(first.UpdatedAt)))
	}
	b.WriteString("| ID | 状态 | 置顶 | 作业 | 标题 | 消息数 | 更新时间 |\n")
	b.WriteString("|----|------|------|------|------|--------|----------|\n")
	for _, issue := range shown {
		b.WriteString(fmt.Sprintf("| %d | %s | %s | %s | %s | %d | %s |\n",
			issue.ID,
			escapeMCPTableCell(issue.Status),
			teacherMCPBool(issue.Pinned),
			escapeMCPTableCell(issue.AssignmentID),
			escapeMCPTableCell(issue.Title),
			issue.MessageCount,
			formatMCPTime(issue.UpdatedAt),
		))
	}
	b.WriteString(fmt.Sprintf("\n共 %d 条，已展示 %d 条。", len(filtered), len(shown)))
	if maxMessages > 0 {
		for _, issue := range shown {
			messages, err := s.store.ListQAMessages(ctx, issue.ID)
			if err != nil {
				b.WriteString(fmt.Sprintf("\n\n## #%d 消息读取失败：%v", issue.ID, err))
				continue
			}
			start := 0
			if len(messages) > maxMessages {
				start = len(messages) - maxMessages
			}
			b.WriteString(fmt.Sprintf("\n\n## #%d %s", issue.ID, strings.TrimSpace(issue.Title)))
			for _, msg := range messages[start:] {
				content := strings.TrimSpace(msg.Content)
				if len([]rune(content)) > 600 {
					content = string([]rune(content)[:600]) + "…"
				}
				b.WriteString(fmt.Sprintf("\n- %s（%s）：%s", msg.Sender, formatMCPTime(msg.CreatedAt), content))
			}
		}
	}
	return b.String(), nil
}

func (s *Server) teacherMCPReplyQAIssue(ctx context.Context, sess *authSession, issueID int, reply string, resolve bool) (string, error) {
	if sess == nil {
		return "", fmt.Errorf("unauthorized")
	}
	reply = strings.TrimSpace(reply)
	if reply == "" {
		return "", fmt.Errorf("reply 不能为空")
	}
	if len([]rune(reply)) > 3000 {
		reply = string([]rune(reply)[:3000])
	}
	issue, err := s.store.GetQAIssueByID(ctx, issueID)
	if err != nil || issue == nil {
		return "", fmt.Errorf("Q&A 不存在")
	}
	if _, err := s.teacherMCPManageCourse(ctx, sess, issue.CourseID); err != nil {
		return "", err
	}
	now := time.Now()
	if _, err := s.store.CreateQAMessage(ctx, &domain.QAMessage{IssueID: issue.ID, Sender: "teacher", Content: reply, CreatedAt: now}); err != nil {
		return "", fmt.Errorf("保存教师回复失败: %w", err)
	}
	if err := s.store.IncrementQAIssueMessageCount(ctx, issue.ID); err != nil {
		return "", fmt.Errorf("更新 Q&A 消息数失败: %w", err)
	}
	statusText := "已保存教师回复"
	if resolve {
		if err := s.store.UpdateQAIssueStatus(ctx, issue.ID, "resolved"); err != nil {
			return "", fmt.Errorf("回复已保存，但标记已解决失败: %w", err)
		}
		statusText = "已保存教师回复，并标记为已解决"
	}
	return fmt.Sprintf("%s：#%d %s", statusText, issue.ID, strings.TrimSpace(issue.Title)), nil
}

func teacherMCPQAIssueFlags(issue domain.QAIssue) string {
	parts := []string{strings.TrimSpace(issue.Status)}
	if issue.Pinned {
		parts = append(parts, "置顶")
	}
	if issue.Hidden {
		parts = append(parts, "隐藏")
	}
	return strings.Join(parts, "、")
}

func teacherMCPBool(v bool) string {
	if v {
		return "是"
	}
	return "否"
}

func formatMCPTime(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	return t.Format("2006-01-02 15:04")
}
