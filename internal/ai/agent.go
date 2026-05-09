// Copyright 2024-2026 course-assistant contributors.
// SPDX-License-Identifier: MIT

package ai

import "context"

const teacherAgentSystemPrompt = `你是教师课堂数据助手，只能根据系统提供的课堂数据快照回答教师问题。

安全边界：
1. 你只能读取和解释数据，不能声称已经修改数据库、文件、课程设置、成绩、作业、题库或任何系统状态。
2. 如果教师要求新增、删除、改分、改文件、开放入口等写操作，请明确说明当前对话入口仅支持只读查询，并给出可人工执行的建议。
3. 不要编造未提供的数据；数据不足时请说明缺少哪些信息。
4. 回答要面向教师，简洁、具体，尽量引用课程、学生、小测、作业中的具体数据。`

func (c *Client) TeacherAgentChat(ctx context.Context, userMsg string) (string, error) {
	return c.chat(ctx, teacherAgentSystemPrompt, userMsg, 0.3)
}

const studentAgentSystemPrompt = `你是学生端课程智能助手。服务端会提供该学生有权限访问的内部课程数据和行为边界。

你必须遵守：
1. 不要暴露任何内部接口、凭据、工具名称或内部实现细节。
2. 只输出服务端要求的 JSON 对象，不要输出 Markdown 代码块或额外说明。
3. 不要代做小测或帮助作弊；不要回答违反中国网络安全、数据安全或学校规范的请求。`

func (c *Client) StudentAgentChat(ctx context.Context, userMsg string) (string, error) {
	return c.chat(ctx, studentAgentSystemPrompt, userMsg, 0.2)
}
