// Copyright 2024-2026 course-assistant contributors.
// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
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

	escapeTableCell := func(s string) string {
		s = strings.ReplaceAll(s, "\r\n", "\n")
		s = strings.ReplaceAll(s, "\n", " ")
		s = strings.ReplaceAll(s, "|", "\\|")
		return strings.TrimSpace(s)
	}

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

	// ── Tool: get_quiz_feedback ──
	getQuizFeedbackTool := mcp.NewTool("get_quiz_feedback",
		mcp.WithDescription("获取某次小测的学生反馈汇总（问卷题分布 + 简答题高频反馈）"),
		mcp.WithString("course_id",
			mcp.Required(),
			mcp.Description("课程ID（数字）"),
		),
		mcp.WithString("quiz_id",
			mcp.Description("题库ID（可选；留空则使用该课程当前加载题库）"),
		),
		mcp.WithString("max_samples",
			mcp.Description("简答题最多展示多少条高频反馈（可选，默认 20）"),
		),
	)
	mcpServer.AddTool(getQuizFeedbackTool, func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
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

		quizID := strings.TrimSpace(request.GetString("quiz_id", ""))
		var q *domain.Quiz
		if quizID == "" {
			s.quizMu.RLock()
			q = s.courseQuizzes[courseID]
			s.quizMu.RUnlock()
			if q == nil {
				return mcp.NewToolResultError("当前课程未加载题库"), nil
			}
		} else {
			q = s.loadCourseQuizFromBank(courseID, course.TeacherID, course.Slug, quizID)
			if q == nil {
				return mcp.NewToolResultError("题库不存在或无法解析"), nil
			}
		}

		maxSamples := 20
		if raw := strings.TrimSpace(request.GetString("max_samples", "")); raw != "" {
			if n, err := strconv.Atoi(raw); err == nil && n > 0 {
				if n > 100 {
					n = 100
				}
				maxSamples = n
			}
		}

		input, err := s.buildAdminSummaryInput(ctx, q, courseID)
		if err != nil {
			return mcp.NewToolResultText("该题库暂无已提交的答题记录"), nil
		}

		var b strings.Builder
		title := strings.TrimSpace(q.Title)
		if title == "" {
			title = q.QuizID
		}
		b.WriteString(fmt.Sprintf("题库：%s（%s）\n", title, q.QuizID))
		b.WriteString(fmt.Sprintf("答题人数（取每人最高分 attempt）：%d\n\n", input.StudentCount))

		hasAny := false
		for _, item := range input.FeedbackItems {
			switch item.Type {
			case "survey":
				if len(item.OptionCounts) == 0 {
					continue
				}
				hasAny = true
				type kv struct {
					k string
					v int
				}
				var pairs []kv
				for k, v := range item.OptionCounts {
					pairs = append(pairs, kv{k: k, v: v})
				}
				sort.Slice(pairs, func(i, j int) bool {
					if pairs[i].v != pairs[j].v {
						return pairs[i].v > pairs[j].v
					}
					return pairs[i].k < pairs[j].k
				})
				b.WriteString("【问卷】" + strings.TrimSpace(item.QuestionID) + "：" + strings.TrimSpace(item.Stem) + "\n")
				for _, p := range pairs {
					b.WriteString(fmt.Sprintf("- %s：%d\n", p.k, p.v))
				}
				b.WriteString("\n")
			case "short_answer":
				if len(item.TextSamples) == 0 {
					continue
				}
				hasAny = true
				freq := map[string]int{}
				for _, t := range item.TextSamples {
					t = strings.TrimSpace(t)
					if t == "" {
						continue
					}
					freq[t]++
				}
				type kv struct {
					k string
					v int
				}
				var pairs []kv
				for k, v := range freq {
					pairs = append(pairs, kv{k: k, v: v})
				}
				sort.Slice(pairs, func(i, j int) bool {
					if pairs[i].v != pairs[j].v {
						return pairs[i].v > pairs[j].v
					}
					return pairs[i].k < pairs[j].k
				})
				if len(pairs) > maxSamples {
					pairs = pairs[:maxSamples]
				}
				b.WriteString("【简答】" + strings.TrimSpace(item.QuestionID) + "：" + strings.TrimSpace(item.Stem) + "\n")
				for _, p := range pairs {
					b.WriteString(fmt.Sprintf("- (%d) %s\n", p.v, p.k))
				}
				b.WriteString("\n")
			}
		}
		if !hasAny {
			b.WriteString("本次小测未收集到问卷/简答反馈（或暂无已提交记录）。")
		}
		return mcp.NewToolResultText(b.String()), nil
	})

	// ── Tool: get_quiz_question_stats ──
	getQuizQuestionStatsTool := mcp.NewTool("get_quiz_question_stats",
		mcp.WithDescription("获取某次小测的逐题统计（正确率、作答数、常见错误选项等）"),
		mcp.WithString("course_id",
			mcp.Required(),
			mcp.Description("课程ID（数字）"),
		),
		mcp.WithString("quiz_id",
			mcp.Description("题库ID（可选；留空则使用该课程当前加载题库）"),
		),
		mcp.WithString("limit",
			mcp.Description("最多返回多少题（可选，默认 10；按正确率从低到高排序）"),
		),
	)
	mcpServer.AddTool(getQuizQuestionStatsTool, func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
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

		quizID := strings.TrimSpace(request.GetString("quiz_id", ""))
		var q *domain.Quiz
		if quizID == "" {
			s.quizMu.RLock()
			q = s.courseQuizzes[courseID]
			s.quizMu.RUnlock()
			if q == nil {
				return mcp.NewToolResultError("当前课程未加载题库"), nil
			}
		} else {
			q = s.loadCourseQuizFromBank(courseID, course.TeacherID, course.Slug, quizID)
			if q == nil {
				return mcp.NewToolResultError("题库不存在或无法解析"), nil
			}
		}

		limit := 10
		if raw := strings.TrimSpace(request.GetString("limit", "")); raw != "" {
			if n, err := strconv.Atoi(raw); err == nil && n > 0 {
				if n > 200 {
					n = 200
				}
				limit = n
			}
		}

		input, err := s.buildAdminSummaryInput(ctx, q, courseID)
		if err != nil {
			return mcp.NewToolResultText("该题库暂无已提交的答题记录"), nil
		}
		if len(input.QuestionStats) == 0 {
			return mcp.NewToolResultText("该题库没有可统计的判分题（single/multi/yes_no）"), nil
		}

		questionByID := map[string]domain.Question{}
		for _, qq := range q.Questions {
			questionByID[qq.ID] = qq
		}

		type row struct {
			id          string
			knowledge   string
			answered    int
			correct     int
			correctRate float64
			stem        string
			wrongTop    string
		}
		rows := make([]row, 0, len(input.QuestionStats))
		for _, st := range input.QuestionStats {
			qq, ok := questionByID[st.QuestionID]
			if !ok {
				continue
			}
			type w struct {
				key   string
				count int
			}
			var wrongs []w
			for key, cnt := range st.AnswerDistribution {
				if strings.TrimSpace(key) == "" || key == qq.CorrectAnswer || cnt <= 0 {
					continue
				}
				wrongs = append(wrongs, w{key: key, count: cnt})
			}
			sort.Slice(wrongs, func(i, j int) bool {
				if wrongs[i].count != wrongs[j].count {
					return wrongs[i].count > wrongs[j].count
				}
				return wrongs[i].key < wrongs[j].key
			})
			top := ""
			if len(wrongs) > 0 {
				max := 3
				if len(wrongs) < max {
					max = len(wrongs)
				}
				parts := make([]string, 0, max)
				for i := 0; i < max; i++ {
					key := wrongs[i].key
					label := key
					for _, opt := range qq.Options {
						if opt.Key == key {
							label = opt.Key + "." + opt.Text
							break
						}
					}
					parts = append(parts, fmt.Sprintf("%s(%d)", label, wrongs[i].count))
				}
				top = strings.Join(parts, ", ")
			}

			stem := strings.TrimSpace(qq.Stem)
			if len([]rune(stem)) > 60 {
				stem = string([]rune(stem)[:60]) + "…"
			}
			rows = append(rows, row{
				id:          st.QuestionID,
				knowledge:   strings.TrimSpace(qq.KnowledgeTag),
				answered:    st.AnsweredCount,
				correct:     st.CorrectCount,
				correctRate: st.CorrectRate,
				stem:        stem,
				wrongTop:    top,
			})
		}

		sort.Slice(rows, func(i, j int) bool {
			if rows[i].correctRate != rows[j].correctRate {
				return rows[i].correctRate < rows[j].correctRate
			}
			if rows[i].answered != rows[j].answered {
				return rows[i].answered > rows[j].answered
			}
			return rows[i].id < rows[j].id
		})
		if len(rows) > limit {
			rows = rows[:limit]
		}

		var b strings.Builder
		title := strings.TrimSpace(q.Title)
		if title == "" {
			title = q.QuizID
		}
		b.WriteString(fmt.Sprintf("题库：%s（%s）\n", title, q.QuizID))
		b.WriteString(fmt.Sprintf("答题人数（取每人最高分 attempt）：%d\n\n", input.StudentCount))
		b.WriteString("| 题号 | 知识点 | 正确率 | 作答 | 常见错误 | 题干 |\n")
		b.WriteString("|------|--------|--------|------|----------|------|\n")
		for _, r := range rows {
			b.WriteString(fmt.Sprintf("| %s | %s | %.1f%% | %d | %s | %s |\n",
				escapeTableCell(r.id),
				escapeTableCell(r.knowledge),
				r.correctRate*100.0,
				r.answered,
				escapeTableCell(r.wrongTop),
				escapeTableCell(r.stem),
			))
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
