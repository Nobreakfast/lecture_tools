// Copyright 2024-2026 course-assistant contributors.
// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"course-assistant/internal/domain"
)

// defaultPromptTemplates defines the built-in defaults for each customizable
// prompt key. Teachers can override these per-key; resetting deletes the
// override and reverts to the default.
var defaultPromptTemplates = map[string]domain.TeacherPromptTemplate{
	"homework_feedback": {
		PromptKey: "homework_feedback",
		Content: `评语风格要求：
- 语气专业、客观、有建设性
- 先肯定优点（1-2句），再指出主要问题（2-3条），最后给出改进方向
- 控制在 150-300 字
- 不要使用空洞套话，引用报告中的具体内容`,
	},
	"homework_pregrade": {
		PromptKey: "homework_pregrade",
		Content: `评分偏好：
- 满分 100 分，按以下维度评分：
  - 报告完整性（标题、摘要、正文、结论）：30%
  - 内容正确性和深度：40%
  - 代码质量（如有）：20%
  - 格式规范性：10%
- 如果报告缺少关键章节，该维度最多给一半分
- 评语需具体说明扣分原因`,
	},
	"quiz_generate": {
		PromptKey: "quiz_generate",
		Content: `出题偏好：
- 判分题在前，调研和开放题在后
- 选择题 4 个选项，干扰项要有合理性
- 每题 explanation 要详细解释正确答案的原因
- 题目难度分布：简单 30%、中等 50%、较难 20%`,
	},
	"class_summary": {
		PromptKey: "class_summary",
		Content: `总结风格：
- 开头用一句话概括本次课堂整体表现
- 数据引用要准确，标明具体正确率和人数
- 教学建议要具体可执行，不要泛泛而谈
- 语言简洁，适合教师快速浏览`,
	},
	"student_analysis": {
		PromptKey: "student_analysis",
		Content: `分析风格：
- 专业、客观、有同理心
- 避免标签化或评判性语言
- 数据引用要具体（引用小测名称、分数、时间）
- 沟通建议要具体可操作`,
	},
}

// AllPromptKeys returns the ordered list of customizable prompt keys.
func AllPromptKeys() []string {
	return []string{
		"homework_feedback",
		"homework_pregrade",
		"quiz_generate",
		"class_summary",
		"student_analysis",
	}
}

// promptKeyLabels maps keys to human-readable Chinese labels.
var promptKeyLabels = map[string]string{
	"homework_feedback": "作业评语生成",
	"homework_pregrade": "作业 AI 预评",
	"quiz_generate":     "题库生成",
	"class_summary":     "课堂总结",
	"student_analysis":  "学生深度分析",
}

// getTeacherPromptOrDefault returns the teacher's custom prompt for the key,
// falling back to the built-in default if no override exists.
func (s *Server) getTeacherPromptOrDefault(ctx context.Context, teacherID, key string) string {
	content, err := s.store.GetTeacherPrompt(ctx, teacherID, key)
	if err == nil && strings.TrimSpace(content) != "" {
		return content
	}
	if def, ok := defaultPromptTemplates[key]; ok {
		return def.Content
	}
	return ""
}

// apiTeacherPromptTemplates handles GET (list all templates with defaults) and
// POST (update or reset a specific key).
func (s *Server) apiTeacherPromptTemplates(w http.ResponseWriter, r *http.Request) {
	sess := s.requireTeacherOrAdmin(r)
	if sess == nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	switch r.Method {
	case http.MethodGet:
		s.handleListPromptTemplates(w, r, sess)
	case http.MethodPost:
		s.handleUpdatePromptTemplate(w, r, sess)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

type promptTemplateItem struct {
	Key        string `json:"key"`
	Label      string `json:"label"`
	Content    string `json:"content"`
	IsDefault  bool   `json:"is_default"`
	DefaultVal string `json:"default_value"`
}

func (s *Server) handleListPromptTemplates(w http.ResponseWriter, r *http.Request, sess *authSession) {
	overrides, _ := s.store.ListTeacherPrompts(r.Context(), sess.TeacherID)
	overrideMap := make(map[string]string, len(overrides))
	for _, o := range overrides {
		overrideMap[o.PromptKey] = o.Content
	}

	items := make([]promptTemplateItem, 0, len(AllPromptKeys()))
	for _, key := range AllPromptKeys() {
		def := defaultPromptTemplates[key]
		item := promptTemplateItem{
			Key:        key,
			Label:      promptKeyLabels[key],
			DefaultVal: def.Content,
			IsDefault:  true,
			Content:    def.Content,
		}
		if override, ok := overrideMap[key]; ok {
			item.Content = override
			item.IsDefault = false
		}
		items = append(items, item)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"templates": items})
}

func (s *Server) handleUpdatePromptTemplate(w http.ResponseWriter, r *http.Request, sess *authSession) {
	var req struct {
		Key     string `json:"key"`
		Content string `json:"content"`
		Reset   bool   `json:"reset"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}
	req.Key = strings.TrimSpace(req.Key)
	if _, ok := defaultPromptTemplates[req.Key]; !ok {
		http.Error(w, "无效的 prompt key", http.StatusBadRequest)
		return
	}

	ctx := r.Context()
	if req.Reset {
		if err := s.store.DeleteTeacherPrompt(ctx, sess.TeacherID, req.Key); err != nil && !errors.Is(err, sql.ErrNoRows) {
			http.Error(w, "重置失败", http.StatusInternalServerError)
			return
		}
	} else {
		content := strings.TrimSpace(req.Content)
		if content == "" {
			http.Error(w, "content 不能为空", http.StatusBadRequest)
			return
		}
		if err := s.store.SetTeacherPrompt(ctx, sess.TeacherID, req.Key, content); err != nil {
			http.Error(w, "保存失败", http.StatusInternalServerError)
			return
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}
