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
		if sess := s.getAuthSessionByToken(token); sess != nil {
			ctx := context.WithValue(r.Context(), mcpAuthKey{}, sess)
			next.ServeHTTP(w, r.WithContext(ctx))
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusUnauthorized)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "unauthorized"})
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
		var courses []domain.Course
		var err error
		if sess.Role == domain.RoleAdmin {
			courses, err = s.store.ListAllCourses(ctx)
		} else {
			courses, err = s.store.ListCoursesByTeacher(ctx, sess.TeacherID)
		}
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
				c.ID,
				escapeTableCell(teacherAgentCourseName(&c)),
				escapeTableCell(c.Slug),
				escapeTableCell(c.InviteCode),
				c.CreatedAt.Format("2006-01-02")))
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
		course, err := s.teacherMCPReadCourse(ctx, sess, courseID)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
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
				escapeTableCell(a.Name),
				escapeTableCell(a.StudentNo),
				escapeTableCell(a.ClassName),
				a.AttemptNo,
				escapeTableCell(string(a.Status)),
				escapeTableCell(score),
				escapeTableCell(a.QuizID)))
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
		course, err := s.teacherMCPReadCourse(ctx, sess, courseID)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
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
				escapeTableCell(item.Name),
				escapeTableCell(item.StudentNo),
				escapeTableCell(item.ClassName),
				escapeTableCell(item.AssignmentID),
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
		if _, err := s.teacherMCPReadCourse(ctx, sess, courseID); err != nil {
			return mcp.NewToolResultError(err.Error()), nil
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
					escapeTableCell(name), escapeTableCell(studentNo), correct, total, attemptNo))
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
		course, err := s.teacherMCPReadCourse(ctx, sess, courseID)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
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
		b.WriteString(fmt.Sprintf("题库：%s（%s）\n", escapeTableCell(title), escapeTableCell(q.QuizID)))
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
		course, err := s.teacherMCPReadCourse(ctx, sess, courseID)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
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
		b.WriteString(fmt.Sprintf("题库：%s（%s）\n", escapeTableCell(title), escapeTableCell(q.QuizID)))
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

	// ── Tool: get_qa_issues ──
	getQAIssuesTool := mcp.NewTool("get_qa_issues",
		mcp.WithDescription("查看某课程的 Q&A 列表和最近消息；用于判断教师应优先处理哪个学生问题"),
		mcp.WithString("course_id",
			mcp.Required(),
			mcp.Description("课程ID（数字）"),
		),
		mcp.WithString("assignment_id",
			mcp.Description("作业编号（可选，留空则查询整门课程）"),
		),
		mcp.WithString("status",
			mcp.Description("筛选状态：open、resolved 或 all（可选，默认 open）"),
		),
		mcp.WithString("limit",
			mcp.Description("最多返回多少条 Q&A（可选，默认 20，最大 100）"),
		),
		mcp.WithString("max_messages",
			mcp.Description("每条 Q&A 展示最近多少条消息（可选，默认 3；设为 0 只看列表）"),
		),
		mcp.WithBoolean("include_hidden",
			mcp.Description("是否包含隐藏 Q&A（可选，默认 false）"),
		),
	)
	mcpServer.AddTool(getQAIssuesTool, func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
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
		limit := 20
		if raw := strings.TrimSpace(request.GetString("limit", "")); raw != "" {
			if n, err := strconv.Atoi(raw); err == nil {
				limit = n
			}
		}
		maxMessages := 3
		if raw := strings.TrimSpace(request.GetString("max_messages", "")); raw != "" {
			if n, err := strconv.Atoi(raw); err == nil {
				maxMessages = n
			}
		}
		text, err := s.teacherMCPQAIssues(ctx, sess, courseID,
			request.GetString("assignment_id", ""),
			request.GetString("status", "open"),
			request.GetBool("include_hidden", false),
			limit,
			maxMessages,
		)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return mcp.NewToolResultText(text), nil
	})

	// ── Tool: reply_qa_issue ──
	replyQAIssueTool := mcp.NewTool("reply_qa_issue",
		mcp.WithDescription("以教师身份回复某条 Q&A；agent 可先润色教师草稿，再调用本工具保存回复并可标记已解决"),
		mcp.WithString("issue_id",
			mcp.Required(),
			mcp.Description("Q&A ID（数字）"),
		),
		mcp.WithString("reply",
			mcp.Required(),
			mcp.Description("要保存给学生看的教师回复内容；如教师给的是要点，请先润色后再提交"),
		),
		mcp.WithBoolean("resolve",
			mcp.Description("回复后是否标记为已解决（可选，默认 true）"),
		),
	)
	mcpServer.AddTool(replyQAIssueTool, func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		sess := s.mcpSessionFromContext(ctx)
		if sess == nil {
			return mcp.NewToolResultError("unauthorized"), nil
		}
		issueIDStr, err := request.RequireString("issue_id")
		if err != nil {
			return mcp.NewToolResultError("缺少 issue_id 参数"), nil
		}
		issueID, err := strconv.Atoi(issueIDStr)
		if err != nil || issueID <= 0 {
			return mcp.NewToolResultError("issue_id 无效"), nil
		}
		reply, err := request.RequireString("reply")
		if err != nil || strings.TrimSpace(reply) == "" {
			return mcp.NewToolResultError("缺少 reply 参数"), nil
		}
		text, err := s.teacherMCPReplyQAIssue(ctx, sess, issueID, reply, request.GetBool("resolve", true))
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return mcp.NewToolResultText(text), nil
	})

	registryCall := func(ctx context.Context, toolName string, args map[string]any) (*mcp.CallToolResult, error) {
		sess := s.mcpSessionFromContext(ctx)
		if sess == nil {
			return mcp.NewToolResultError("unauthorized"), nil
		}
		text, err := s.callAgentTool(ctx, toolName, agentToolContext{Session: sess, Platform: false, Confirmed: true}, args)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return mcp.NewToolResultText(text), nil
	}

	getCourseContextTool := mcp.NewTool("get_course_context",
		mcp.WithDescription("读取课程概览、小测、作业和材料摘要"),
		mcp.WithString("course_id", mcp.Required(), mcp.Description("课程ID（数字）")),
	)
	mcpServer.AddTool(getCourseContextTool, func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return registryCall(ctx, "get_course_context", map[string]any{"course_id": request.GetString("course_id", "")})
	})

	searchAgentMentionsTool := mcp.NewTool("search_agent_mentions",
		mcp.WithDescription("搜索教师 Agent 可引用的课程、小测、资料、学生、作业和 Q&A"),
		mcp.WithString("course_id", mcp.Description("课程ID（可选）")),
		mcp.WithString("q", mcp.Description("关键词（可选）")),
		mcp.WithString("limit", mcp.Description("最多返回数量（可选，默认 80）")),
	)
	mcpServer.AddTool(searchAgentMentionsTool, func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return registryCall(ctx, "search_agent_mentions", map[string]any{"course_id": request.GetString("course_id", ""), "q": request.GetString("q", ""), "limit": request.GetString("limit", "")})
	})

	getQuizBankListTool := mcp.NewTool("get_quiz_bank_list",
		mcp.WithDescription("列出课程题库库，供选择历史小测"),
		mcp.WithString("course_id", mcp.Required(), mcp.Description("课程ID（数字）")),
	)
	mcpServer.AddTool(getQuizBankListTool, func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return registryCall(ctx, "get_quiz_bank_list", map[string]any{"course_id": request.GetString("course_id", "")})
	})

	readQuizBankYAMLTool := mcp.NewTool("read_quiz_bank_yaml",
		mcp.WithDescription("读取课程题库库中的 YAML 内容，用于参考历史小测"),
		mcp.WithString("course_id", mcp.Required(), mcp.Description("课程ID（数字）")),
		mcp.WithString("quiz_id", mcp.Required(), mcp.Description("题库ID")),
	)
	mcpServer.AddTool(readQuizBankYAMLTool, func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return registryCall(ctx, "read_quiz_bank_yaml", map[string]any{"course_id": request.GetString("course_id", ""), "quiz_id": request.GetString("quiz_id", "")})
	})

	getStudentProfileTool := mcp.NewTool("get_student_profile",
		mcp.WithDescription("读取单个学生跨小测、作业和 Q&A 的画像"),
		mcp.WithString("course_id", mcp.Required(), mcp.Description("课程ID（数字）")),
		mcp.WithString("student_no", mcp.Description("学号")),
		mcp.WithString("name", mcp.Description("姓名")),
		mcp.WithString("class_name", mcp.Description("班级")),
	)
	mcpServer.AddTool(getStudentProfileTool, func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return registryCall(ctx, "get_student_profile", map[string]any{"course_id": request.GetString("course_id", ""), "student_no": request.GetString("student_no", ""), "name": request.GetString("name", ""), "class_name": request.GetString("class_name", "")})
	})

	getAttemptDetailTool := mcp.NewTool("get_attempt_detail",
		mcp.WithDescription("读取某次小测提交详情和错题"),
		mcp.WithString("course_id", mcp.Required(), mcp.Description("课程ID（数字）")),
		mcp.WithString("attempt_id", mcp.Required(), mcp.Description("答题记录ID")),
	)
	mcpServer.AddTool(getAttemptDetailTool, func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return registryCall(ctx, "get_attempt_detail", map[string]any{"course_id": request.GetString("course_id", ""), "attempt_id": request.GetString("attempt_id", "")})
	})

	getAssignmentContextTool := mcp.NewTool("get_assignment_context",
		mcp.WithDescription("读取作业说明、提交概览、评分和预评状态"),
		mcp.WithString("course_id", mcp.Required(), mcp.Description("课程ID（数字）")),
		mcp.WithString("assignment_id", mcp.Required(), mcp.Description("作业编号")),
	)
	mcpServer.AddTool(getAssignmentContextTool, func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return registryCall(ctx, "get_assignment_context", map[string]any{"course_id": request.GetString("course_id", ""), "assignment_id": request.GetString("assignment_id", "")})
	})

	listMaterialsTool := mcp.NewTool("list_materials",
		mcp.WithDescription("列出课程资料文件"),
		mcp.WithString("course_id", mcp.Required(), mcp.Description("课程ID（数字）")),
	)
	mcpServer.AddTool(listMaterialsTool, func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return registryCall(ctx, "list_materials", map[string]any{"course_id": request.GetString("course_id", "")})
	})

	readMaterialTextTool := mcp.NewTool("read_material_text",
		mcp.WithDescription("读取课程资料文本，PDF 会提取正文"),
		mcp.WithString("course_id", mcp.Required(), mcp.Description("课程ID（数字）")),
		mcp.WithString("material_file", mcp.Required(), mcp.Description("资料文件名")),
	)
	mcpServer.AddTool(readMaterialTextTool, func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return registryCall(ctx, "read_material_text", map[string]any{"course_id": request.GetString("course_id", ""), "material_file": request.GetString("material_file", "")})
	})

	draftQuizFromPromptTool := mcp.NewTool("draft_quiz_from_prompt",
		mcp.WithDescription("根据教师提示生成题库 YAML 草稿，不自动保存"),
		mcp.WithString("course_id", mcp.Description("课程ID（可选；提供后会按需检索历史题库和课程上下文）")),
		mcp.WithString("prompt", mcp.Required(), mcp.Description("生成要求")),
	)
	mcpServer.AddTool(draftQuizFromPromptTool, func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return registryCall(ctx, "draft_quiz_from_prompt", map[string]any{"course_id": request.GetString("course_id", ""), "prompt": request.GetString("prompt", "")})
	})

	draftQuizFromMaterialTool := mcp.NewTool("draft_quiz_from_material",
		mcp.WithDescription("根据课程 PDF 资料生成题库 YAML 草稿，不自动保存"),
		mcp.WithString("course_id", mcp.Required(), mcp.Description("课程ID（数字）")),
		mcp.WithString("material_file", mcp.Required(), mcp.Description("PDF 资料文件名")),
		mcp.WithString("quiz_id", mcp.Description("题库ID（可选）")),
		mcp.WithString("quiz_title", mcp.Description("题库标题（可选）")),
		mcp.WithString("question_count", mcp.Description("题目数量（可选，默认 8）")),
		mcp.WithString("base_prompt", mcp.Description("额外生成要求（可选）")),
	)
	mcpServer.AddTool(draftQuizFromMaterialTool, func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return registryCall(ctx, "draft_quiz_from_material", map[string]any{
			"course_id":      request.GetString("course_id", ""),
			"material_file":  request.GetString("material_file", ""),
			"quiz_id":        request.GetString("quiz_id", ""),
			"quiz_title":     request.GetString("quiz_title", ""),
			"question_count": request.GetString("question_count", ""),
			"base_prompt":    request.GetString("base_prompt", ""),
		})
	})

	autofillQuizYAMLTool := mcp.NewTool("autofill_quiz_yaml",
		mcp.WithDescription("补全题库 YAML 的 explanation 和 knowledge_tag，不自动保存"),
		mcp.WithString("course_id", mcp.Description("课程ID（可选；提供后会参考课程上下文）")),
		mcp.WithString("yaml", mcp.Required(), mcp.Description("题库 YAML 内容")),
	)
	mcpServer.AddTool(autofillQuizYAMLTool, func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return registryCall(ctx, "autofill_quiz_yaml", map[string]any{"course_id": request.GetString("course_id", ""), "yaml": request.GetString("yaml", "")})
	})

	draftClassSummaryTool := mcp.NewTool("draft_class_summary",
		mcp.WithDescription("生成当前或指定小测的课堂总结草稿，不自动覆盖已保存总结"),
		mcp.WithString("course_id", mcp.Required(), mcp.Description("课程ID（数字）")),
		mcp.WithString("quiz_id", mcp.Description("题库ID（可选）")),
	)
	mcpServer.AddTool(draftClassSummaryTool, func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return registryCall(ctx, "draft_class_summary", map[string]any{"course_id": request.GetString("course_id", ""), "quiz_id": request.GetString("quiz_id", "")})
	})

	draftHistorySummaryTool := mcp.NewTool("draft_history_summary",
		mcp.WithDescription("生成课程历史小测趋势总结草稿，不自动覆盖已保存总结"),
		mcp.WithString("course_id", mcp.Required(), mcp.Description("课程ID（数字）")),
	)
	mcpServer.AddTool(draftHistorySummaryTool, func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return registryCall(ctx, "draft_history_summary", map[string]any{"course_id": request.GetString("course_id", "")})
	})

	draftHomeworkFeedbackTool := mcp.NewTool("draft_homework_feedback",
		mcp.WithDescription("为单个作业提交生成评语草稿，不自动保存"),
		mcp.WithString("course_id", mcp.Required(), mcp.Description("课程ID（数字）")),
		mcp.WithString("submission_id", mcp.Required(), mcp.Description("作业提交ID")),
		mcp.WithString("note", mcp.Description("教师简短批注意见（可选）")),
	)
	mcpServer.AddTool(draftHomeworkFeedbackTool, func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return registryCall(ctx, "draft_homework_feedback", map[string]any{"course_id": request.GetString("course_id", ""), "submission_id": request.GetString("submission_id", ""), "note": request.GetString("note", "")})
	})

	setQuizEntryOpenTool := mcp.NewTool("set_quiz_entry_open",
		mcp.WithDescription("开启或关闭课程小测入口，会修改系统状态；请仅在教师明确要求后调用"),
		mcp.WithString("course_id", mcp.Required(), mcp.Description("课程ID（数字）")),
		mcp.WithBoolean("open", mcp.Required(), mcp.Description("true=开启，false=关闭")),
	)
	mcpServer.AddTool(setQuizEntryOpenTool, func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return registryCall(ctx, "set_quiz_entry_open", map[string]any{"course_id": request.GetString("course_id", ""), "open": request.GetBool("open", false)})
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
