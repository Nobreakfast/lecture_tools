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
