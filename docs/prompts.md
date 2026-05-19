# 系统 Prompt 文档

本文档列出课程助手平台中所有 AI 调用的 prompt 结构。每个 AI 操作由**系统 prompt（system prompt）**和**用户 prompt（user prompt）**组成。系统 prompt 由平台维护，用户 prompt 中的"教师模板"部分可由教师自定义。

---

## 架构总览

```
┌─────────────────────────────────────────────┐
│ globalAgentSystemPrompt（全局安全策略）        │
│ + 当前任务/角色策略（taskPrompt）              │
│ = 最终 system prompt                         │
├─────────────────────────────────────────────┤
│ user prompt = 工具上下文 + 教师自定义模板      │
│              + 具体数据/消息                   │
└─────────────────────────────────────────────┘
```

- `globalAgentSystemPrompt`：所有 AI 调用共享的安全边界和输出格式规范。
- 各功能的 task prompt：定义具体任务的角色和行为约束。
- 教师自定义模板（`teacher_prompt_templates`）：教师可按功能覆盖默认的输出格式/评分偏好。

---

## 1. 全局安全策略

**文件**：`internal/ai/agent_policy.go`  
**变量**：`globalAgentSystemPrompt`

所有 AI 调用的 system prompt 前缀，包含：
- 角色定义（课程助手平台内的教学 Agent）
- 6 条安全边界规则
- 输出格式规范（JSON / YAML / Markdown）

---

## 2. 教师 Agent 对话

**文件**：`internal/ai/agent.go`  
**变量**：`teacherAgentSystemPrompt`

| 属性 | 值 |
|------|-----|
| temperature | 0.3 |
| 组合方式 | globalAgentSystemPrompt + teacherAgentSystemPrompt |
| 功能 | 教师与课堂数据助手的自由对话 |

核心策略：工具结果是事实来源、只生成草稿需教师复核、不执行未确认写操作、引用具体数据。

---

## 3. 学生 Agent 对话

**文件**：`internal/ai/agent.go`  
**变量**：`studentAgentSystemPrompt`

| 属性 | 值 |
|------|-----|
| temperature | 0.2 |
| 组合方式 | globalAgentSystemPrompt + studentAgentSystemPrompt |
| 输出格式 | 严格 JSON：`{"action":"...","answer":"...","qa_title":"...","qa_summary":"..."}` |

核心策略：不暴露内部实现、不代做小测、优先复用 Q&A、遵循中国网络安全法规。

---

## 4. 学生小测反馈

**文件**：`internal/ai/summary.go`  
**变量**：`systemPrompt`

| 属性 | 值 |
|------|-----|
| temperature | 0.7 |
| 输出格式 | JSON：strengths, weaknesses, next_actions, priority_level, encouragement |
| 功能 | 根据学生错题生成个性化学习反馈 |

---

## 5. 课堂总结（教师）

**文件**：`internal/ai/admin_summary.go`  
**变量**：`adminSystemPrompt`

| 属性 | 值 |
|------|-----|
| temperature | 0.4 |
| 输出格式 | JSON：answer_analysis, feedback_summary, teaching_suggestions |
| 功能 | 根据全班答题统计为教师生成教学总结报告 |

---

## 6. 历史趋势总结

**文件**：`internal/ai/admin_summary.go`  
**变量**：`historySystemPrompt`

| 属性 | 值 |
|------|-----|
| temperature | 0.4 |
| 输出格式 | JSON：overall_trend, performance_insights, teaching_suggestions |
| 功能 | 分析多次小测的纵向表现趋势 |

---

## 7. 题库生成

**文件**：`internal/ai/quiz_generate.go`  
**变量**：`quizGenerateSystemPrompt`

| 属性 | 值 |
|------|-----|
| temperature | 0.7（生成）/ 0.55（从 PDF 初始化） |
| 输出格式 | 纯 YAML |
| 功能 | 根据教师需求生成课堂小测题库 YAML |

核心约束：题型字段硬要求（id/type/stem/options/correct_answer）、LaTeX 数学公式、explanation 和 knowledge_tag 必填。

---

## 8. 题库补全

**文件**：`internal/ai/quiz_generate.go`  
**变量**：`quizAutoFillSystemPrompt`

| 属性 | 值 |
|------|-----|
| temperature | 0.4 |
| 输出格式 | 纯 YAML（完整输出） |
| 功能 | 补全已有题库中缺少的 explanation 和 knowledge_tag |

---

## 9. 作业评语生成

**文件**：`internal/ai/homework_grade.go`  
**变量**：`homeworkGradeFeedbackSystemPrompt`  
**教师可自定义模板 key**：`homework_feedback`

| 属性 | 值 |
|------|-----|
| temperature | 0.4 |
| 输出格式 | 纯文本（不给分数） |
| 功能 | 根据学生报告正文和教师简短批注生成评语 |

教师自定义模板影响：评语风格、长度偏好、关注维度（如代码质量 vs 报告结构）。

---

## 10. 作业预评（AI 评分）

**文件**：`internal/ai/homework_grade.go`  
**变量**：`homeworkPregradeSystemPrompt`  
**教师可自定义模板 key**：`homework_pregrade`

| 属性 | 值 |
|------|-----|
| temperature | 0.2 |
| 输出格式 | JSON：`{"suggested_score": 数字, "feedback": "评语"}` |
| 功能 | 根据学生报告和教师评分维度生成建议分 + 评语草稿 |

教师自定义模板影响：评分维度、扣分规则、满分标准、评语格式。

---

## 11. 简答题评分

**文件**：`internal/ai/short_answer_grade.go`  
**变量**：`shortAnswerGradeSystemPrompt`

| 属性 | 值 |
|------|-----|
| temperature | 0.1 |
| 输出格式 | JSON：`{"score": 0-1, "feedback": "反馈"}` |
| 功能 | 对小测简答题自动评分 |

---

## 12. 学生深度分析

**文件**：`internal/ai/student_analysis.go`  
**变量**：`studentAnalysisSystemPrompt`

| 属性 | 值 |
|------|-----|
| temperature | 0.4 |
| 输出格式 | JSON（5 个字段）|
| 功能 | 根据学生全课程行为数据生成深度分析报告 |

---

## 教师可自定义 Prompt 模板

以下是教师可以覆盖的 prompt 模板 key 和默认值：

| Key | 用途 | 默认行为 |
|-----|------|---------|
| `homework_feedback` | 作业评语生成时的输出风格 | 专业、客观，指出优点和改进建议 |
| `homework_pregrade` | 作业预评的评分维度和规则 | 按报告完整性、正确性、代码质量评分 |
| `quiz_generate` | 题库生成时的教师额外偏好 | 判分题在前、调研在后、简洁 |
| `class_summary` | 课堂总结的输出风格 | 数据驱动、引用具体正确率 |
| `student_analysis` | 学生分析报告风格 | 专业客观、有同理心 |

教师在"设置 > AI 偏好"中可以编辑这些模板。恢复默认按钮将重置为系统内置值。

---

## Prompt 组合流程示例

以作业预评为例：

```
最终 system prompt = globalAgentSystemPrompt
                   + "\n\n当前任务/角色策略：\n"
                   + homeworkPregradeSystemPrompt

最终 user prompt = JSON(HomeworkGradeFeedbackInput)
                 + "\n\n【教师评分偏好】\n"
                 + teacher_prompt_templates["homework_pregrade"]
```

教师模板为空或不存在时，使用系统内置默认值。

---

## API 接口

### GET /api/teacher/prompt-templates

返回所有可自定义的 prompt 模板列表（含当前值和是否为默认）。

响应示例：
```json
{
  "templates": [
    {
      "key": "homework_feedback",
      "label": "作业评语生成",
      "content": "当前使用的模板内容...",
      "is_default": true,
      "default_value": "系统默认模板内容..."
    }
  ]
}
```

### POST /api/teacher/prompt-templates

更新或重置某个 prompt 模板。

请求体（更新）：
```json
{"key": "homework_pregrade", "content": "新的模板内容..."}
```

请求体（恢复默认）：
```json
{"key": "homework_pregrade", "reset": true}
```

---

## 数据库表

```sql
CREATE TABLE teacher_prompt_templates (
    teacher_id TEXT NOT NULL,
    prompt_key TEXT NOT NULL,
    content    TEXT NOT NULL DEFAULT '',
    updated_at TEXT NOT NULL DEFAULT '',
    PRIMARY KEY(teacher_id, prompt_key)
);
```
