// Copyright 2024-2026 course-assistant contributors.
// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"course-assistant/internal/domain"

	"github.com/google/uuid"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// mcpAuthKey is the context key used to store the authenticated session
// for MCP tool handlers.
type mcpAuthKey struct{}

// mcpAuthMiddleware wraps an HTTP handler with token-based authentication
// suitable for MCP clients (which do not send browser cookies).
// It tries, in order: Authorization Bearer header, ?token= query param,
// and finally the auth_token cookie.
func (s *Server) mcpAuthMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := ""
		if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
			token = strings.TrimPrefix(h, "Bearer ")
		}
		if token == "" {
			token = r.URL.Query().Get("token")
		}
		if token == "" {
			if c, err := r.Cookie("auth_token"); err == nil {
				token = c.Value
			}
		}
		sess := s.getAuthSessionByToken(token)
		if sess == nil {
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.WriteHeader(http.StatusUnauthorized)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "unauthorized"})
			return
		}
		ctx := context.WithValue(r.Context(), mcpAuthKey{}, sess)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// mcpSessionFromContext tries to retrieve the auth session from the context.
// It first looks for the direct context value (set during SSE connection).
// If that's missing, it falls back to extracting the token from the mcp-go
// session ID and looking it up in the server's auth store.
func (s *Server) mcpSessionFromContext(ctx context.Context) *authSession {
	if sess, ok := ctx.Value(mcpAuthKey{}).(*authSession); ok {
		return sess
	}
	if mcpSess := server.ClientSessionFromContext(ctx); mcpSess != nil {
		sessionID := mcpSess.SessionID()
		if idx := strings.Index(sessionID, ":"); idx > 0 {
			token := sessionID[:idx]
			return s.getAuthSessionByToken(token)
		}
	}
	return nil
}

// newMCPSSEServer creates an SSE-backed MCP server with all course-assistant
// tools registered.  The returned server must be mounted under /mcp (or the
// equivalent path-prefix stripped path) so that the SSE endpoint is /mcp/sse
// and the message endpoint is /mcp/message.
func (s *Server) newMCPSSEServer() *server.SSEServer {
	mcpServer := server.NewMCPServer(
		"course-assistant",
		"1.0.0",
		server.WithRecovery(),
	)

	// ── Tool: list_courses ──
	listCoursesTool := mcp.NewTool("list_courses",
		mcp.WithDescription("列出教师的所有课程，包含课程ID、名称、标识和邀请码"),
	)
	mcpServer.AddTool(listCoursesTool, func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		sess := s.mcpSessionFromContext(ctx)
		if sess == nil {
			return mcp.NewToolResultError("unauthorized"), nil
		}
		courses, err := s.store.ListCoursesByTeacher(ctx, sess.TeacherID)
		if err != nil {
			return mcp.NewToolResultError("读取课程列表失败: " + err.Error()), nil
		}
		if len(courses) == 0 {
			return mcp.NewToolResultText("暂无课程"), nil
		}
		var b strings.Builder
		b.WriteString("| ID | 课程名称 | 标识 | 邀请码 | 创建时间 |\n")
		b.WriteString("|----|----------|------|--------|----------|\n")
		for _, c := range courses {
			b.WriteString(fmt.Sprintf("| %d | %s | %s | %s | %s |\n",
				c.ID, c.Name, c.Slug, c.InviteCode, c.CreatedAt.Format("2006-01-02")))
		}
		b.WriteString(fmt.Sprintf("\n共 %d 门课程", len(courses)))
		return mcp.NewToolResultText(b.String()), nil
	})

	// ── Tool: get_quiz_attempts ──
	getQuizAttemptsTool := mcp.NewTool("get_quiz_attempts",
		mcp.WithDescription("获取某课程的答题记录，包含每位学生的最佳成绩"),
		mcp.WithString("course_id",
			mcp.Required(),
			mcp.Description("课程ID（数字）"),
		),
	)
	mcpServer.AddTool(getQuizAttemptsTool, func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		sess := s.mcpSessionFromContext(ctx)
		if sess == nil {
			return mcp.NewToolResultError("unauthorized"), nil
		}
		courseIDStr, err := request.RequireString("course_id")
		if err != nil {
			return mcp.NewToolResultError("缺少 course_id 参数"), nil
		}
		courseID, err := strconv.Atoi(courseIDStr)
		if err != nil {
			return mcp.NewToolResultError("course_id 无效"), nil
		}
		course, err := s.store.GetCourse(ctx, courseID)
		if err != nil || course == nil {
			return mcp.NewToolResultError("课程不存在"), nil
		}
		if sess.Role != domain.RoleAdmin && course.TeacherID != sess.TeacherID {
			return mcp.NewToolResultError("无权限访问此课程"), nil
		}

		items, err := s.store.ListAttemptsByCourse(ctx, courseID)
		if err != nil {
			return mcp.NewToolResultError("读取答题记录失败: " + err.Error()), nil
		}

		s.quizMu.RLock()
		q := s.courseQuizzes[courseID]
		s.quizMu.RUnlock()

		bestItems := s.teacherCourseBestAttempts(ctx, course, q, items)
		if len(bestItems) == 0 {
			return mcp.NewToolResultText("该课程暂无答题记录"), nil
		}

		var b strings.Builder
		b.WriteString("| 姓名 | 学号 | 班级 | 答题次数 | 状态 | 得分 | 题库ID |\n")
		b.WriteString("|------|------|------|----------|------|------|--------|\n")
		for _, item := range bestItems {
			a := item.Attempt
			score := "-"
			if item.QuizLoaded {
				score = fmt.Sprintf("%d/%d", item.Correct, item.Total)
			}
			b.WriteString(fmt.Sprintf("| %s | %s | %s | %d | %s | %s | %s |\n",
				a.Name, a.StudentNo, a.ClassName, a.AttemptNo,
				string(a.Status), score, a.QuizID))
		}
		b.WriteString(fmt.Sprintf("\n共 %d 条记录", len(bestItems)))
		if q != nil {
			b.WriteString(fmt.Sprintf("，当前加载题库: %s", q.QuizID))
		}
		return mcp.NewToolResultText(b.String()), nil
	})

	// ── Tool: get_homework_submissions ──
	getHomeworkSubmissionsTool := mcp.NewTool("get_homework_submissions",
		mcp.WithDescription("获取某课程的作业提交情况"),
		mcp.WithString("course_id",
			mcp.Required(),
			mcp.Description("课程ID（数字）"),
		),
		mcp.WithString("assignment_id",
			mcp.Description("作业编号（可选，留空则查询所有作业）"),
		),
	)
	mcpServer.AddTool(getHomeworkSubmissionsTool, func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		sess := s.mcpSessionFromContext(ctx)
		if sess == nil {
			return mcp.NewToolResultError("unauthorized"), nil
		}
		courseIDStr, err := request.RequireString("course_id")
		if err != nil {
			return mcp.NewToolResultError("缺少 course_id 参数"), nil
		}
		courseID, err := strconv.Atoi(courseIDStr)
		if err != nil {
			return mcp.NewToolResultError("course_id 无效"), nil
		}
		course, err := s.store.GetCourse(ctx, courseID)
		if err != nil || course == nil {
			return mcp.NewToolResultError("课程不存在"), nil
		}
		if sess.Role != domain.RoleAdmin && course.TeacherID != sess.TeacherID {
			return mcp.NewToolResultError("无权限访问此课程"), nil
		}

		assignmentID := request.GetString("assignment_id", "")
		items, err := s.store.ListHomeworkSubmissions(ctx, courseID, course.Slug, assignmentID)
		if err != nil {
			return mcp.NewToolResultError("读取作业列表失败: " + err.Error()), nil
		}
		if len(items) == 0 {
			return mcp.NewToolResultText("该课程暂无作业提交记录"), nil
		}

		var b strings.Builder
		b.WriteString("| 姓名 | 学号 | 班级 | 作业编号 | 报告 | 代码 | 补充 | 更新时间 |\n")
		b.WriteString("|------|------|------|----------|------|------|------|----------|\n")
		for _, item := range items {
			report := "✗"
			if item.ReportOriginalName != "" {
				report = "✓"
			}
			code := "✗"
			if item.CodeOriginalName != "" {
				code = "✓"
			}
			extra := "✗"
			if item.ExtraOriginalName != "" {
				extra = "✓"
			}
			b.WriteString(fmt.Sprintf("| %s | %s | %s | %s | %s | %s | %s | %s |\n",
				item.Name, item.StudentNo, item.ClassName, item.AssignmentID,
				report, code, extra, item.UpdatedAt.Format("2006-01-02 15:04")))
		}
		b.WriteString(fmt.Sprintf("\n共 %d 条记录", len(items)))
		return mcp.NewToolResultText(b.String()), nil
	})

	// ── Tool: get_summary_stats ──
	getSummaryStatsTool := mcp.NewTool("get_summary_stats",
		mcp.WithDescription("获取某课程的答题统计数据（当前加载的题库）"),
		mcp.WithString("course_id",
			mcp.Required(),
			mcp.Description("课程ID（数字）"),
		),
	)
	mcpServer.AddTool(getSummaryStatsTool, func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		sess := s.mcpSessionFromContext(ctx)
		if sess == nil {
			return mcp.NewToolResultError("unauthorized"), nil
		}
		courseIDStr, err := request.RequireString("course_id")
		if err != nil {
			return mcp.NewToolResultError("缺少 course_id 参数"), nil
		}
		courseID, err := strconv.Atoi(courseIDStr)
		if err != nil {
			return mcp.NewToolResultError("course_id 无效"), nil
		}
		course, err := s.store.GetCourse(ctx, courseID)
		if err != nil || course == nil {
			return mcp.NewToolResultError("课程不存在"), nil
		}
		if sess.Role != domain.RoleAdmin && course.TeacherID != sess.TeacherID {
			return mcp.NewToolResultError("无权限访问此课程"), nil
		}

		s.quizMu.RLock()
		q := s.courseQuizzes[courseID]
		s.quizMu.RUnlock()
		if q == nil {
			return mcp.NewToolResultError("当前课程未加载题库"), nil
		}

		stats := s.buildQuizRawStats(ctx, q, courseID)
		if errStr, ok := stats["error"].(string); ok && errStr != "" {
			return mcp.NewToolResultError("统计失败: " + errStr), nil
		}

		// Marshal to JSON for reliable type extraction then format as Markdown.
		statsJSON, _ := json.Marshal(stats)
		var structured struct {
			StudentCount int              `json:"student_count"`
			AvgScore     float64          `json:"avg_score"`
			AvgTotal     float64          `json:"avg_total"`
			Students     []map[string]any `json:"students"`
		}
		_ = json.Unmarshal(statsJSON, &structured)

		var b strings.Builder
		b.WriteString(fmt.Sprintf("答题人数: %d\n", structured.StudentCount))
		b.WriteString(fmt.Sprintf("平均得分: %.1f / %.1f\n\n", structured.AvgScore, structured.AvgTotal))
		if len(structured.Students) > 0 {
			b.WriteString("| 姓名 | 学号 | 正确数 | 总数 | 答题次数 |\n")
			b.WriteString("|------|------|--------|------|----------|\n")
			for _, row := range structured.Students {
				name, _ := row["name"].(string)
				studentNo, _ := row["student_no"].(string)
				correct := 0
				if v, ok := row["correct"].(float64); ok {
					correct = int(v)
				}
				total := 0
				if v, ok := row["total"].(float64); ok {
					total = int(v)
				}
				attemptNo := 0
				if v, ok := row["attempt_no"].(float64); ok {
					attemptNo = int(v)
				}
				b.WriteString(fmt.Sprintf("| %s | %s | %d | %d | %d |\n",
					name, studentNo, correct, total, attemptNo))
			}
		}
		return mcp.NewToolResultText(b.String()), nil
	})

	sseServer := server.NewSSEServer(mcpServer,
		server.WithBasePath("/mcp"),
		server.WithSessionIDGenerator(func(ctx context.Context, r *http.Request) (string, error) {
			// Embed the auth token into the session ID so that subsequent
			// message POSTs can recover the session without re-authenticating.
			token := ""
			if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
				token = strings.TrimPrefix(h, "Bearer ")
			}
			if token == "" {
				token = r.URL.Query().Get("token")
			}
			if token == "" {
				if c, err := r.Cookie("auth_token"); err == nil {
					token = c.Value
				}
			}
			if token != "" {
				return token + ":" + uuid.New().String(), nil
			}
			return uuid.New().String(), nil
		}),
	)
	return sseServer
}
