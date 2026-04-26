// Copyright 2024-2026 course-assistant contributors.
// SPDX-License-Identifier: MIT

package ai

import (
	"context"
)

const quizGenerateSystemPrompt = `你是一个课堂题库生成助手。请根据教师的要求生成 YAML 格式的题库。

输出格式要求：
- 顶层字段：quiz_id, title, questions
- 每题必须有：id（如 q1, q2, q3 ... 每题递增，不可重复）, type, stem
- 支持题型：single_choice, multi_choice, yes_no, survey, short_answer
- single_choice: 必须有 options（每个 option 含 key 和 text）和 correct_answer（单个 key，如 "B"）
- multi_choice: 必须有 options 和 correct_answer（英文逗号分隔 key，如 "A,B,C"）
- yes_no: options 固定为 key Y/N，必须有 correct_answer
- survey: 必须有 options，不写 correct_answer；如需多选可加 allow_multiple: true
- short_answer: 不写 options 和 correct_answer，可写 reference_answer
- short_answer 可选 short_answer_mode 字段，控制学生回答方式：text=仅文字输入 / image=仅上传图片 / code=仅代码粘贴 / text_image=文字+图片上传（省略则根据题干自动检测）
- 每道题必须填写 explanation（详细解析，解释为什么正确答案是对的，帮助学生理解）
- 每道题必须填写 knowledge_tag（该题考察的知识点名称，简短，如"凸函数判定"、"梯度下降"）

可选字段（按需使用）：
- fixed_position: true — 固定该题位置，不参与随机排序
- pool_tag: "标签" — 题池标签，用于随机抽题

数学公式格式：
- 题干和选项中的数学公式必须使用 LaTeX 格式
- 行内公式用 $...$ 或 \(...\)
- 独立公式用 $$...$$
- 例如：$f(x) = x^2$、$\nabla f(x)$、$$\min_{x} f(x)$$

题目顺序默认"判分题在前，调研/开放题在后"。
文案简洁，课堂可直接使用。
只输出纯 YAML 内容，不要输出 markdown 代码块或其他文字。`

const quizAutoFillSystemPrompt = `你是一个课堂题库补全助手。教师已编写好题目，但部分题目缺少解析(explanation)和知识点标签(knowledge_tag)。

请补全缺失的字段，规则如下：
1. 不要修改已有的任何字段内容（题干、选项、答案、short_answer_mode、fixed_position 等保持原样）
2. 对于缺少 explanation 的题目，根据题目内容和正确答案写详细解析
3. 对于缺少 knowledge_tag 的题目，根据题目内容填写简短的知识点名称
4. 数学公式使用 LaTeX 格式（$...$）
5. 保持 YAML 格式不变，保留所有已有字段

只输出完整的 YAML 内容，不要输出其他文字。`

func (c *Client) GenerateQuiz(ctx context.Context, prompt string) (string, error) {
	return c.chat(ctx, quizGenerateSystemPrompt, prompt, 0.7)
}

func (c *Client) AutoFillQuiz(ctx context.Context, yamlContent string) (string, error) {
	return c.chat(ctx, quizAutoFillSystemPrompt, yamlContent, 0.4)
}