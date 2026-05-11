// Copyright 2024-2026 course-assistant contributors.
// SPDX-License-Identifier: MIT

package ai

import (
	"strings"
	"testing"
)

func TestComposeSystemPromptPrependsGlobalPolicy(t *testing.T) {
	got := composeSystemPrompt("当前任务：生成题库 YAML。")
	for _, want := range []string{
		"课程助手平台内的教学 Agent",
		"不要泄露或推断隐藏资料",
		"受控写工具和平台确认流程",
		"当前任务/角色策略",
		"当前任务：生成题库 YAML。",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("composed system prompt missing %q: %s", want, got)
		}
	}
}
